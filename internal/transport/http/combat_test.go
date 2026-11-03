package httptransport

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReportHTTPValidatesBeforeDisabledProvider(t *testing.T) {
	for _, tc := range []struct {
		suffix string
		status int
	}{
		{"short", 400},
		{"DEMOCombat000001", 503},
		{"DEMOCombat000001/fights", 503},
		{"DEMOCombat000001/fights/1/analysis?actor_id=1", 503},
		{"DEMOCombat000001/fights/0/analysis?actor_id=1", 400},
		{"DEMOCombat000001/fights/1/analysis?actor_id=0", 400},
		{"DEMOCombat000001/fights/1/analysis?actor_id=1&actor_id=2", 400},
		{"DEMOCombat000001/fights/1/analysis?actor_id=2147483648", 400},
		{"DEMOCombat000001/fights/1/analysis?actor_id=01", 400},
		{"DEMOCombat000001?locale=invalid", 400},
	} {
		t.Run(tc.suffix, func(t *testing.T) {
			h, store := handlerForTest()
			w := httptest.NewRecorder()
			h.Router().ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/reports/"+tc.suffix, nil))
			if w.Code != tc.status || store.calls != 0 {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
			if tc.status == 503 && !strings.Contains(w.Body.String(), "COMBAT_NOT_CONFIGURED") {
				t.Fatal(w.Body.String())
			}
		})
	}
}
