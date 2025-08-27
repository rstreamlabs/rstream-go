// See LICENSE file in the project root for license information.

package rstream

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
)

type stringFilter struct {
	Exact *string  `json:"exact,omitempty"`
	OneOf []string `json:"oneof,omitempty"`
	Regex *string  `json:"regex,omitempty"`
}

type filterNode map[string]json.RawMessage

func MatchPermissions(props *TunnelProperties, raw []byte, action string) (bool, error) {
	if props == nil {
		return false, errors.New("nil props")
	}
	var perms map[string]json.RawMessage
	if err := json.Unmarshal(raw, &perms); err != nil {
		return false, err
	}
	actRaw, ok := perms[action]
	if !ok {
		return true, nil
	}
	var b bool
	if err := json.Unmarshal(actRaw, &b); err == nil {
		return b, nil
	}
	var actObj map[string]json.RawMessage
	if err := json.Unmarshal(actRaw, &actObj); err != nil {
		return false, err
	}
	filtersRaw, ok := actObj["filters"]
	if !ok {
		return true, nil
	}
	var node filterNode
	if err := json.Unmarshal(filtersRaw, &node); err != nil {
		return false, err
	}
	propsMap, err := structToMap(props)
	if err != nil {
		return false, err
	}
	return evalFilter(node, propsMap)
}

func structToMap(v any) (map[string]any, error) {
	buf, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(buf, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func evalFilter(node filterNode, props map[string]any) (bool, error) {
	for k, raw := range node {
		switch k {
		case "AND":
			var arr []filterNode
			if err := json.Unmarshal(raw, &arr); err != nil {
				return false, err
			}
			for _, n := range arr {
				ok, err := evalFilter(n, props)
				if err != nil || !ok {
					return ok, err
				}
			}
		case "OR":
			var arr []filterNode
			if err := json.Unmarshal(raw, &arr); err != nil {
				return false, err
			}
			match := false
			for _, n := range arr {
				ok, err := evalFilter(n, props)
				if err != nil {
					return false, err
				}
				if ok {
					match = true
					break
				}
			}
			if !match {
				return false, nil
			}
		default:
			propVal, exists := props[k]
			if !exists {
				return false, nil
			}
			ok, err := matchValue(raw, propVal)
			if err != nil || !ok {
				return ok, err
			}
		}
	}
	return true, nil
}

func matchValue(raw json.RawMessage, prop any) (bool, error) {
	var sf stringFilter
	if json.Unmarshal(raw, &sf) == nil && (sf.Exact != nil || sf.Regex != nil || len(sf.OneOf) > 0) {
		return matchStringFilter(sf, prop)
	}
	var str string
	if json.Unmarshal(raw, &str) == nil {
		return matchStringFilter(stringFilter{Exact: &str}, prop)
	}
	var bl bool
	if json.Unmarshal(raw, &bl) == nil {
		switch v := prop.(type) {
		case bool:
			return v == bl, nil
		case *bool:
			return v != nil && *v == bl, nil
		}
		return false, nil
	}
	var num float64
	if json.Unmarshal(raw, &num) == nil {
		switch v := prop.(type) {
		case float64:
			return v == num, nil
		case uint16:
			return float64(v) == num, nil
		case *uint16:
			return v != nil && float64(*v) == num, nil
		}
		return false, nil
	}
	var sub filterNode
	if json.Unmarshal(raw, &sub) == nil {
		mp, ok := prop.(map[string]any)
		if !ok {
			if mss, ok2 := prop.(map[string]string); ok2 {
				mp = make(map[string]any, len(mss))
				for k, v := range mss {
					mp[k] = v
				}
			} else {
				return false, nil
			}
		}
		return evalFilter(sub, mp)
	}
	return false, fmt.Errorf("unsupported filter %s", string(raw))
}

func matchStringFilter(sf stringFilter, prop any) (bool, error) {
	ps, ok := toString(prop)
	if !ok {
		return false, nil
	}
	if sf.Exact != nil {
		return ps == *sf.Exact, nil
	}
	if len(sf.OneOf) > 0 {
		for _, s := range sf.OneOf {
			if ps == s {
				return true, nil
			}
		}
		return false, nil
	}
	if sf.Regex != nil {
		re, err := regexp.Compile(*sf.Regex)
		if err != nil {
			return false, err
		}
		return re.MatchString(ps), nil
	}
	return false, errors.New("empty string filter")
}

func toString(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case *string:
		if t == nil {
			return "", false
		}
		return *t, true
	case bool:
		if t {
			return "true", true
		}
		return "false", true
	case *bool:
		if t == nil {
			return "", false
		}
		if *t {
			return "true", true
		}
		return "false", true
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64), true
	case uint16:
		return strconv.Itoa(int(t)), true
	case *uint16:
		if t == nil {
			return "", false
		}
		return strconv.Itoa(int(*t)), true
	default:
		return "", false
	}
}
