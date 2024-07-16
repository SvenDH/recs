package events

import (
	"encoding/json"
	"fmt"
)

type Op int

const (
	Create Op = iota
	Delete
	Add
	Set
	Remove
)

type Message struct {
	Channel string      `json:"chn,omitempty"`
	Idx     uint64      `json:"idx,omitempty"`
	Op      Op          `json:"op,omitempty"`
	Entity  uint64      `json:"ent,omitempty"`
	Key     string      `json:"key,omitempty"`
	Value   interface{} `json:"val,omitempty"`
}

func (l *Message) ToJsonPatch(n string) string {
	switch l.Op {
	case Create:
		var v []byte
		var err error
		if l.Value == nil {
			v = []byte("{}")
		} else {
			v, err = json.Marshal(l.Value)
			if err != nil {
				return ""
			}
		}
		return fmt.Sprintf(`{"op":"add","path":"/%s/%d","value":%s}`, l.Channel, l.Entity, v)
	case Delete:
		return fmt.Sprintf(`{"op":"remove","path":":"/%s/%d"}`, l.Channel, l.Entity)
	case Add:
		v, err := json.Marshal(l.Value)
		if err != nil {
			return ""
		}
		return fmt.Sprintf(`{"op":"add","path":"/%s/%d/%s","value":%s}`, l.Channel, l.Entity, l.Key, v)
	case Set:
		v, err := json.Marshal(l.Value)
		if err != nil {
			return ""
		}
		return fmt.Sprintf(`{"op":"replace","path":"/%s/%d/%s","value":%s}`, l.Channel, l.Entity, l.Key, v)
	case Remove:
		return fmt.Sprintf(`{"op":"remove","path":"/%s/%d/%s"}`, l.Channel, l.Entity, l.Key)
	}
	return ""
}
