package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

func TestSurveyRowsCutoverAndRollback(t *testing.T) {
	databaseURL := os.Getenv("CONTROL_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("CONTROL_TEST_DATABASE_URL is not set")
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil || !strings.HasSuffix(parsed.Path, "_test") {
		t.Fatal("survey row test requires a disposable *_test database")
	}
	repo, err := OpenPostgres(databaseURL, 4, 4, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	id := "survey-rows-test-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	ctx := WithSurveyID(context.Background(), id)
	if _, err := repo.CreateSurvey(ctx, domain.SurveyMetadata{ID: id, Name: "Isolated survey", Description: "test"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = repo.db.ExecContext(context.Background(), `DELETE FROM control_survey_state WHERE survey_id=$1`, id)
	})
	now := time.Now().UTC()
	if err := repo.UpdateSurvey(ctx, func(state *domain.SurveyState) error {
		state.Settings = domain.SurveySettings{Open: true, Version: 3}
		state.Guests["submitted"] = domain.SurveyGuest{Nickname: "访客甲", ExpiresAt: now.Add(7 * 24 * time.Hour), ResponseID: "receipt"}
		state.Guests["draft"] = domain.SurveyGuest{Nickname: "访客乙", ExpiresAt: now.Add(7 * 24 * time.Hour)}
		state.Responses["receipt"] = domain.SurveyResponse{ID: "receipt", Nickname: "访客甲", CreatedAt: now, SurveySubmission: domain.SurveySubmission{Version: 1, Answers: map[string]domain.SurveyAnswer{"q1": {Text: "保留的回答"}}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CutoverSurveyRows(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := repo.CutoverSurveyRows(ctx, id); err != nil {
		t.Fatal("repeat cutover must be idempotent:", err)
	}
	var version int
	var raw []byte
	if err := repo.db.QueryRowContext(ctx, `SELECT state,storage_version FROM control_survey_state WHERE survey_id=$1`, id).Scan(&raw, &version); err != nil {
		t.Fatal(err)
	}
	if version != 2 || strings.Contains(string(raw), "保留的回答") {
		t.Fatal("legacy response was not moved out of the hot row")
	}
	var wg sync.WaitGroup
	for i := 1; i <= 2; i++ {
		wg.Add(1)
		go func(revision int) {
			defer wg.Done()
			handled, err := repo.UpdateSurveyGuest(ctx, "draft", func(_ domain.SurveySettings, guest domain.SurveyGuest, exists bool) (domain.SurveyGuest, error) {
				if !exists {
					return guest, fmt.Errorf("draft guest missing")
				}
				if int64(revision) > guest.DraftRevision {
					guest.DraftRevision = int64(revision)
					guest.Draft = &domain.SurveySubmission{Version: 1, Answers: map[string]domain.SurveyAnswer{"q2": {Text: "草稿"}}}
				}
				return guest, nil
			})
			if !handled || err != nil {
				t.Errorf("concurrent draft update: handled=%v err=%v", handled, err)
			}
		}(i)
	}
	wg.Wait()
	state, err := repo.ReadSurvey(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.Guests["draft"].DraftRevision != 2 || state.Responses["receipt"].Answers["q1"].Text != "保留的回答" || !state.Settings.Open {
		t.Fatal("cutover lost guest, response, or settings")
	}
	var claimCount int
	if err := repo.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM control_survey_nickname_claim WHERE survey_id=$1`, id).Scan(&claimCount); err != nil || claimCount != 2 {
		t.Fatalf("nickname claims: %d %v", claimCount, err)
	}
	if err := repo.RestoreLegacySurveyRows(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := repo.db.QueryRowContext(ctx, `SELECT storage_version FROM control_survey_state WHERE survey_id=$1`, id).Scan(&version); err != nil || version != 1 {
		t.Fatalf("rollback version: %d %v", version, err)
	}
	state, err = repo.ReadSurvey(ctx)
	if err != nil || state.Guests["draft"].DraftRevision != 2 || state.Responses["receipt"].Answers["q1"].Text != "保留的回答" {
		t.Fatal("rollback lost survey data", err)
	}
	if err := repo.CutoverSurveyRows(ctx, id); err != nil {
		t.Fatal("repeat cutover after rollback:", err)
	}
	var entryCount int
	if err := repo.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM control_survey_entry WHERE survey_id=$1 AND kind='response'`, id).Scan(&entryCount); err != nil && err != sql.ErrNoRows {
		t.Fatal(err)
	}
	if entryCount != 1 {
		t.Fatal("repeat cutover duplicated responses")
	}
}

func BenchmarkSurveyDraftStorage(b *testing.B) {
	databaseURL := os.Getenv("CONTROL_TEST_DATABASE_URL")
	if databaseURL == "" {
		b.Skip("CONTROL_TEST_DATABASE_URL is not set")
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil || !strings.HasSuffix(parsed.Path, "_test") {
		b.Fatal("benchmark requires a disposable *_test database")
	}
	repo, err := OpenPostgres(databaseURL, 4, 4, time.Now().UTC())
	if err != nil {
		b.Fatal(err)
	}
	defer repo.Close()
	for _, count := range []int{0, 100, 1000} {
		for _, rows := range []bool{false, true} {
			label := "legacy"
			if rows {
				label = "rows"
			}
			b.Run(fmt.Sprintf("%s/%d", label, count), func(b *testing.B) {
				id := fmt.Sprintf("survey-bench-%d", time.Now().UnixNano())
				ctx := WithSurveyID(context.Background(), id)
				if _, err := repo.CreateSurvey(ctx, domain.SurveyMetadata{ID: id, Name: "Benchmark survey"}); err != nil {
					b.Fatal(err)
				}
				defer repo.db.ExecContext(context.Background(), `DELETE FROM control_survey_state WHERE survey_id=$1`, id)
				now := time.Now().UTC()
				if err := repo.UpdateSurvey(ctx, func(state *domain.SurveyState) error {
					state.Guests["writer"] = domain.SurveyGuest{Nickname: "writer", ExpiresAt: now.Add(7 * 24 * time.Hour)}
					for i := 0; i < count; i++ {
						answers := map[string]domain.SurveyAnswer{}
						for q := 1; q <= 16; q++ {
							answers[fmt.Sprintf("q%d", q)] = domain.SurveyAnswer{Text: strings.Repeat("x", 80)}
						}
						key := fmt.Sprintf("response-%04d", i)
						state.Responses[key] = domain.SurveyResponse{ID: key, Nickname: fmt.Sprintf("guest-%04d", i), CreatedAt: now, SurveySubmission: domain.SurveySubmission{Version: 1, Answers: answers}}
					}
					return nil
				}); err != nil {
					b.Fatal(err)
				}
				if rows {
					if err := repo.CutoverSurveyRows(ctx, id); err != nil {
						b.Fatal(err)
					}
				}
				draftAnswers := map[string]domain.SurveyAnswer{}
				for q := 1; q <= 16; q++ {
					draftAnswers[fmt.Sprintf("q%d", q)] = domain.SurveyAnswer{Text: strings.Repeat("d", 80)}
				}
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if rows {
						handled, err := repo.UpdateSurveyGuest(ctx, "writer", func(_ domain.SurveySettings, guest domain.SurveyGuest, exists bool) (domain.SurveyGuest, error) {
							if !exists {
								return guest, fmt.Errorf("writer missing")
							}
							guest.DraftRevision++
							guest.Draft = &domain.SurveySubmission{Version: 1, Answers: draftAnswers}
							return guest, nil
						})
						if !handled || err != nil {
							b.Fatal("fast draft update:", handled, err)
						}
					} else if err := repo.UpdateSurvey(ctx, func(state *domain.SurveyState) error {
						guest := state.Guests["writer"]
						guest.DraftRevision++
						guest.Draft = &domain.SurveySubmission{Version: 1, Answers: draftAnswers}
						state.Guests["writer"] = guest
						return nil
					}); err != nil {
						b.Fatal(err)
					}
				}
				b.StopTimer()
			})
		}
	}
}
