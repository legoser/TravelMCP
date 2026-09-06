package sync

import (
	"encoding/json"
	"fmt"
	"sort"
)

type Override struct {
	EntityType string `json:"entity_type"`
	System     string `json:"system"`
	Code       string `json:"code"`
	Field      string `json:"field"`
	Value      any    `json:"value"`
	ActorID    int64  `json:"actor_id"`
}

func (o Override) Key() string {
	return o.EntityType + "|" + o.System + "|" + o.Code + "|" + o.Field
}

func (o Override) Validate() error {
	if o.EntityType == "" || o.System == "" || o.Code == "" || o.Field == "" {
		return fmt.Errorf("override: пустые entity_type/system/code/field")
	}
	if o.ActorID == 0 {
		return fmt.Errorf("override: actor_id обязателен")
	}
	return nil
}

func MarshalOverrides(list []Override) ([]byte, error) {
	cp := append([]Override{}, list...)
	sort.Slice(cp, func(i, j int) bool { return cp[i].Key() < cp[j].Key() })
	for _, o := range cp {
		if err := o.Validate(); err != nil {
			return nil, err
		}
	}
	return json.MarshalIndent(cp, "", "  ")
}

func UnmarshalOverrides(data []byte) ([]Override, error) {
	var list []Override
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, o := range list {
		if err := o.Validate(); err != nil {
			return nil, err
		}
		if seen[o.Key()] {
			return nil, fmt.Errorf("override: дубль ключа %s", o.Key())
		}
		seen[o.Key()] = true
	}
	return list, nil
}
