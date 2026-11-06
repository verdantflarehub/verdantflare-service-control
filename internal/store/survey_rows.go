package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

type surveyRowSnapshot struct {
	challenges map[string]domain.SurveyChallenge
	guests     map[string]domain.SurveyGuest
	rates      map[string]domain.SurveyRate
	responses  map[string]domain.SurveyResponse
}

func (p *Postgres) SurveyStorageVersion(ctx context.Context, id string) (int, error) {
	var version int
	err := p.db.QueryRowContext(ctx, `SELECT storage_version FROM control_survey_state WHERE survey_id=$1`, id).Scan(&version)
	if err == sql.ErrNoRows {
		return 0, surveyMissing()
	}
	return version, err
}

// SurveyGuestUpdater is an optional v2 path for draft writes. A false handled
// result means the survey still uses the legacy aggregate.
type SurveyGuestUpdater interface {
	UpdateSurveyGuest(context.Context, string, func(domain.SurveySettings, domain.SurveyGuest, bool) (domain.SurveyGuest, error)) (bool, error)
}

func (p *Postgres) UpdateSurveyGuest(ctx context.Context, key string, update func(domain.SurveySettings, domain.SurveyGuest, bool) (domain.SurveyGuest, error)) (bool, error) {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return true, err
	}
	defer tx.Rollback()
	id := SurveyID(ctx)
	var compact []byte
	var version int
	if err := tx.QueryRowContext(ctx, `SELECT state,storage_version FROM control_survey_state WHERE survey_id=$1 FOR UPDATE`, id).Scan(&compact, &version); err != nil {
		if err == sql.ErrNoRows {
			return true, surveyMissing()
		}
		return true, err
	}
	if version == 1 {
		return false, nil
	}
	if version != 2 {
		return true, fmt.Errorf("unsupported survey storage version %d", version)
	}
	var state domain.SurveyState
	if err := json.Unmarshal(compact, &state); err != nil {
		return true, err
	}
	var oldRaw []byte
	err = tx.QueryRowContext(ctx, `SELECT payload FROM control_survey_entry WHERE survey_id=$1 AND kind='guest' AND entry_key=$2 FOR UPDATE`, id, key).Scan(&oldRaw)
	if err != nil && err != sql.ErrNoRows {
		return true, err
	}
	exists := err == nil
	var guest domain.SurveyGuest
	if exists {
		if err := json.Unmarshal(oldRaw, &guest); err != nil {
			return true, err
		}
	}
	beforeRaw, err := json.Marshal(guest)
	if err != nil {
		return true, err
	}
	updated, err := update(state.Settings, guest, exists)
	if err != nil {
		return true, err
	}
	raw, err := json.Marshal(updated)
	if err != nil {
		return true, err
	}
	if !exists || bytes.Equal(beforeRaw, raw) {
		return true, tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, `UPDATE control_survey_entry SET payload=$1,payload_bytes=$2 WHERE survey_id=$3 AND kind='guest' AND entry_key=$4`, raw, len(raw), id, key); err != nil {
		return true, err
	}
	var bytes int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(payload_bytes + octet_length(entry_key) + 3),0) FROM control_survey_entry WHERE survey_id=$1`, id).Scan(&bytes); err != nil {
		return true, err
	}
	if bytes+int64(len(compact)) > 64<<20 {
		return true, domain.NewError(503, "survey_capacity", "本次问卷存储已达上限，请联系组织方")
	}
	return true, tx.Commit()
}

func snapshotSurveyRows(state domain.SurveyState) surveyRowSnapshot {
	return surveyRowSnapshot{maps.Clone(state.Challenges), maps.Clone(state.Guests), maps.Clone(state.Rates), maps.Clone(state.Responses)}
}

func loadSurveyRows(ctx context.Context, tx *sql.Tx, id string, state *domain.SurveyState) error {
	rows, err := tx.QueryContext(ctx, `SELECT kind, entry_key, payload FROM control_survey_entry WHERE survey_id=$1`, id)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var kind, key string
		var raw []byte
		if err := rows.Scan(&kind, &key, &raw); err != nil {
			return err
		}
		switch kind {
		case "challenge":
			var value domain.SurveyChallenge
			if err := json.Unmarshal(raw, &value); err != nil {
				return err
			}
			state.Challenges[key] = value
		case "guest":
			var value domain.SurveyGuest
			if err := json.Unmarshal(raw, &value); err != nil {
				return err
			}
			state.Guests[key] = value
		case "rate":
			var value domain.SurveyRate
			if err := json.Unmarshal(raw, &value); err != nil {
				return err
			}
			state.Rates[key] = value
		case "response":
			var value domain.SurveyResponse
			if err := json.Unmarshal(raw, &value); err != nil {
				return err
			}
			state.Responses[key] = value
		default:
			return fmt.Errorf("unknown survey row kind %q", kind)
		}
	}
	return rows.Err()
}

func persistSurveyMap[T any](ctx context.Context, tx *sql.Tx, id, kind string, before, after map[string]T) error {
	for key, value := range after {
		if old, ok := before[key]; ok && reflect.DeepEqual(old, value) {
			continue
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO control_survey_entry (survey_id, kind, entry_key, payload, payload_bytes)
			VALUES ($1,$2,$3,$4,$5) ON CONFLICT (survey_id,kind,entry_key)
			DO UPDATE SET payload=EXCLUDED.payload, payload_bytes=EXCLUDED.payload_bytes`, id, kind, key, raw, len(raw)); err != nil {
			return err
		}
	}
	for key := range before {
		if _, exists := after[key]; !exists {
			if _, err := tx.ExecContext(ctx, `DELETE FROM control_survey_entry WHERE survey_id=$1 AND kind=$2 AND entry_key=$3`, id, kind, key); err != nil {
				return err
			}
		}
	}
	return nil
}

type surveyClaim struct {
	guestKey   string
	responseID string
	expiresAt  time.Time
}

func surveyClaims(state domain.SurveyState, now time.Time) (map[string]surveyClaim, error) {
	claims := make(map[string]surveyClaim, len(state.Guests)+len(state.Responses))
	for id, response := range state.Responses {
		expires := response.CreatedAt.Add(180 * 24 * time.Hour)
		if !now.Before(expires) {
			continue
		}
		key := domain.SurveyNicknameKey(response.Nickname)
		if _, exists := claims[key]; exists {
			return nil, fmt.Errorf("duplicate active survey nickname")
		}
		claims[key] = surveyClaim{responseID: id, expiresAt: expires}
	}
	for id, guest := range state.Guests {
		if !now.Before(guest.ExpiresAt) {
			continue
		}
		key := domain.SurveyNicknameKey(guest.Nickname)
		if prior, exists := claims[key]; exists {
			if guest.ResponseID == prior.responseID && prior.responseID != "" {
				continue
			}
			return nil, fmt.Errorf("duplicate active survey nickname")
		}
		claims[key] = surveyClaim{guestKey: id, expiresAt: guest.ExpiresAt}
	}
	return claims, nil
}

func persistSurveyClaims(ctx context.Context, tx *sql.Tx, id string, state domain.SurveyState) error {
	desired, err := surveyClaims(state, time.Now().UTC())
	if err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT nickname_key, guest_key, response_id, expires_at FROM control_survey_nickname_claim WHERE survey_id=$1`, id)
	if err != nil {
		return err
	}
	old := make(map[string]surveyClaim)
	for rows.Next() {
		var key string
		var guest, response sql.NullString
		var expires time.Time
		if err := rows.Scan(&key, &guest, &response, &expires); err != nil {
			rows.Close()
			return err
		}
		old[key] = surveyClaim{guestKey: guest.String, responseID: response.String, expiresAt: expires}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for key, claim := range desired {
		if prior, ok := old[key]; ok && prior.guestKey == claim.guestKey && prior.responseID == claim.responseID && prior.expiresAt.Equal(claim.expiresAt) {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO control_survey_nickname_claim (survey_id,nickname_key,guest_key,response_id,expires_at)
			VALUES ($1,$2,NULLIF($3,''),NULLIF($4,''),$5) ON CONFLICT (survey_id,nickname_key)
			DO UPDATE SET guest_key=EXCLUDED.guest_key,response_id=EXCLUDED.response_id,expires_at=EXCLUDED.expires_at`, id, key, claim.guestKey, claim.responseID, claim.expiresAt); err != nil {
			return err
		}
	}
	for key := range old {
		if _, ok := desired[key]; !ok {
			if _, err := tx.ExecContext(ctx, `DELETE FROM control_survey_nickname_claim WHERE survey_id=$1 AND nickname_key=$2`, id, key); err != nil {
				return err
			}
		}
	}
	return nil
}

func persistSurveyRows(ctx context.Context, tx *sql.Tx, id string, before surveyRowSnapshot, state domain.SurveyState) error {
	if err := persistSurveyMap(ctx, tx, id, "challenge", before.challenges, state.Challenges); err != nil {
		return err
	}
	if err := persistSurveyMap(ctx, tx, id, "guest", before.guests, state.Guests); err != nil {
		return err
	}
	if err := persistSurveyMap(ctx, tx, id, "rate", before.rates, state.Rates); err != nil {
		return err
	}
	if err := persistSurveyMap(ctx, tx, id, "response", before.responses, state.Responses); err != nil {
		return err
	}
	if err := persistSurveyClaims(ctx, tx, id, state); err != nil {
		return err
	}
	var bytes int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(payload_bytes + octet_length(entry_key) + 3),0) FROM control_survey_entry WHERE survey_id=$1`, id).Scan(&bytes); err != nil {
		return err
	}
	compact := domain.SurveyState{Metadata: state.Metadata, Settings: state.Settings}
	raw, err := json.Marshal(compact)
	if err != nil {
		return err
	}
	if bytes+int64(len(raw)) > 64<<20 {
		return domain.NewError(503, "survey_capacity", "本次问卷存储已达上限，请联系组织方")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE control_survey_state SET state=$1,updated_at=NOW() WHERE survey_id=$2`, raw, id); err != nil {
		return err
	}
	return nil
}

// CutoverSurveyRows is invoked only after every Control replica understands
// storage_version=2. The old aggregate and all new rows switch atomically.
func (p *Postgres) CutoverSurveyRows(ctx context.Context, id string) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var raw []byte
	var version int
	if err := tx.QueryRowContext(ctx, `SELECT state,storage_version FROM control_survey_state WHERE survey_id=$1 FOR UPDATE`, id).Scan(&raw, &version); err != nil {
		return err
	}
	if version == 2 {
		return tx.Commit()
	}
	if version != 1 {
		return fmt.Errorf("unsupported survey storage version %d", version)
	}
	var state domain.SurveyState
	if err := json.Unmarshal(raw, &state); err != nil {
		return err
	}
	initializeSurveyMaps(&state)
	if _, err := tx.ExecContext(ctx, `DELETE FROM control_survey_entry WHERE survey_id=$1`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM control_survey_nickname_claim WHERE survey_id=$1`, id); err != nil {
		return err
	}
	if err := persistSurveyRows(ctx, tx, id, surveyRowSnapshot{}, state); err != nil {
		return err
	}
	verified := domain.SurveyState{Metadata: state.Metadata, Settings: state.Settings}
	initializeSurveyMaps(&verified)
	if err := loadSurveyRows(ctx, tx, id, &verified); err != nil {
		return err
	}
	if !reflect.DeepEqual(state, verified) {
		return errors.New("survey row cutover verification failed")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE control_survey_state SET storage_version=2 WHERE survey_id=$1`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// RestoreLegacySurveyRows is a guarded rollback path for a table-aware
// release. It must not run while any replica may still write version 2.
func (p *Postgres) RestoreLegacySurveyRows(ctx context.Context, id string) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var raw []byte
	var version int
	if err := tx.QueryRowContext(ctx, `SELECT state,storage_version FROM control_survey_state WHERE survey_id=$1 FOR UPDATE`, id).Scan(&raw, &version); err != nil {
		return err
	}
	if version == 1 {
		return tx.Commit()
	}
	if version != 2 {
		return fmt.Errorf("unsupported survey storage version %d", version)
	}
	var state domain.SurveyState
	if err := json.Unmarshal(raw, &state); err != nil {
		return err
	}
	initializeSurveyMaps(&state)
	if err := loadSurveyRows(ctx, tx, id, &state); err != nil {
		return err
	}
	full, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if len(full) > 64<<20 {
		return domain.NewError(503, "survey_capacity", "回退后的问卷状态超过旧版容量上限")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE control_survey_state SET state=$1,storage_version=1,updated_at=NOW() WHERE survey_id=$2`, full, id); err != nil {
		return err
	}
	return tx.Commit()
}
