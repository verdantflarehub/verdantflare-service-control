package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/verdantflarehub/verdantflare-service-control/internal/control"
	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
)

func TestPublicationMCPFailurePreservesFailureClass(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		status   int
		code     string
		internal bool
	}{
		{name: "business error", err: domain.NewError(409, "APP_REVISION_CHANGED", "conflict"), status: 409, code: "APP_REVISION_CHANGED"},
		{name: "invalid arguments", err: &json.UnmarshalTypeError{Value: "string", Type: nil}, status: http.StatusBadRequest, code: "INVALID_ARGUMENTS"},
		{name: "storage failure", err: errors.New("database unavailable"), status: http.StatusServiceUnavailable, code: "PUBLISH_UNAVAILABLE", internal: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			failure, internal := publicationMCPFailure(tc.err)
			if failure.Status != tc.status || failure.Code != tc.code || internal != tc.internal {
				t.Fatalf("failure = %+v, internal = %t", failure, internal)
			}
		})
	}
}

func TestModelMCPFailureUsesModelErrorVocabulary(t *testing.T) {
	for _, tc := range []struct {
		err      error
		status   int
		code     string
		internal bool
	}{
		{err: domain.NewError(503, "model_gateway_ops_unavailable", "unconfigured"), status: 503, code: "model_gateway_ops_unavailable"},
		{err: &json.SyntaxError{}, status: 400, code: "MODEL_ARGUMENTS_INVALID"},
		{err: errors.New("gateway timeout"), status: 503, code: "MODEL_CONFIG_UNAVAILABLE", internal: true},
	} {
		failure, internal := hubMCPFailure(tc.err, control.MCPTokenPurposeModel)
		if failure.Status != tc.status || failure.Code != tc.code || internal != tc.internal {
			t.Fatalf("failure = %+v, internal = %t", failure, internal)
		}
	}
}
