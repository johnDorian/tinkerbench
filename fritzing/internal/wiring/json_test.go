package wiring

import "encoding/json"

func jsonOf(w Wiring) (string, error) { b, err := json.Marshal(w); return string(b), err }
