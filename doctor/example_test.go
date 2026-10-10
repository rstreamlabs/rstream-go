// See LICENSE file in the project root for license information.

package doctor_test

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rstreamlabs/rstream-go/config"
	"github.com/rstreamlabs/rstream-go/doctor"
)

func ExampleRun() {
	resolution, err := config.ResolveFromEnv(config.ClientEnvOptions{Context: "production"})
	if err != nil {
		fmt.Println(err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	report, err := doctor.Run(ctx, resolution.Resolved, doctor.Options{})
	for _, check := range report.Checks {
		fmt.Printf("%s: %s: %s\n", check.Name, check.Status, check.Message)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		fmt.Println("Diagnostics stopped at the deadline; results are partial.")
	} else if errors.Is(err, doctor.ErrChecksFailed) {
		fmt.Printf("%d diagnostic checks failed.\n", report.Summary.Fail)
	}
}
