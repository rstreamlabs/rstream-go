//go:build rstream_fips

// See LICENSE file in the project root for license information.

package doctor_test

import (
	"errors"
	"testing"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
	"github.com/rstreamlabs/rstream-go/doctor"
)

func TestFIPSProfileDoctorEnforcesRuntime(t *testing.T) {
	report, err := doctor.Run(t.Context(), config.Resolved{}, doctor.Options{})
	if !errors.Is(err, doctor.ErrChecksFailed) {
		t.Fatalf("expected an offline diagnostic failure: %v", err)
	}
	checks := checksByName(report)
	if rstream.RequireFIPS() != nil {
		if len(checks) != 1 || checks["fips"].Status != doctor.StatusFail {
			t.Fatalf("invalid FIPS runtime was accepted: %+v", checks)
		}
	} else if checks["token"].Status != doctor.StatusFail {
		t.Fatalf("valid FIPS runtime did not run diagnostics: %+v", checks)
	}
}
