package overview

import (
	"slices"
	"strings"
	"testing"
)

// Rules пула строятся только для сервисов с DatabasePool и считают насыщение по
// пулу процесса, а не суммой по replica set.
func TestPoolAlerts(t *testing.T) {
	withAllPresent(t)
	want := []string{"saturation", "queued", "wait", "timeouts", "cleared"}
	for _, o := range Services() {
		g, err := ServiceAlerts(o)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, r := range g.Rules {
			if s, ok := strings.CutPrefix(*r.Uid, "nomnom-"+o.Context+"-db-pool-"); ok {
				got = append(got, s)
			}
		}
		switch {
		case o.DatabasePool && o.Database == MongoDB:
			if !slices.Equal(got, want) {
				t.Errorf("%s: pool rules %v, want %v", o.Context, got, want)
			}
		case o.DatabasePool:
			// cleared — только MongoDB CMAP; у PostgreSQL пулов такого события нет.
			if postgres := want[:len(want)-1]; !slices.Equal(got, postgres) {
				t.Errorf("%s: pool rules %v, want %v", o.Context, got, postgres)
			}
		case !o.DatabasePool && len(got) > 0:
			t.Errorf("%s: pool rules %v without DatabasePool", o.Context, got)
		}
	}
	sat := poolSaturation(Scope{Services: []string{"restaurants"}, Literal: true})
	for _, part := range []string{`db_client_connection_state="used"`, "sum by (" + poolBy + ")"} {
		if !strings.Contains(sat, part) {
			t.Errorf("saturation %q: нет %q", sat, part)
		}
	}
	if _, err := Analyze(sat); err != nil {
		t.Error(err)
	}
}
