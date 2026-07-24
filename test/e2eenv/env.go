// See LICENSE file in the project root for license information.

package e2eenv

import (
	"fmt"
	"os"
	"strconv"
)

const allowCrossRegionRoutingEnv = "RSTREAM_E2E_ALLOW_CROSS_REGION_ROUTING"

func AllowCrossRegionRouting() (*bool, error) {
	value, ok := os.LookupEnv(allowCrossRegionRoutingEnv)
	if !ok {
		return nil, nil
	}
	enabled, err := strconv.ParseBool(value)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", allowCrossRegionRoutingEnv, err)
	}
	return &enabled, nil
}
