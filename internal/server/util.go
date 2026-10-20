package server

import (
	"encoding/json"
	"strconv"
)

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

func trimFloat(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }
