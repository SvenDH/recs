package events

import (
	"fmt"
	"strings"
	"strconv"
	"encoding/json"
)

type Op int

const (
	Noop Op = iota
	Create
	Delete
	Add
	Set
	Remove
	Snapshot
)

type Message struct {
	Idx     uint64
	Op      Op
	Entity  uint64
	Key     string
	Value   interface{}
}

func (l Message) Serialize() string {
	m := fmt.Sprintf("%d %d ", l.Idx, l.Op)
	if l.Entity != 0 {
		id, gen := uint32(l.Entity >> 32), uint32(l.Entity & 0xFFFFFFFF)
		m += fmt.Sprintf("%d %d ", id, gen)
	}
	if l.Key != "" {
		m += l.Key
		if l.Value != nil { m += " "}
	}
	if l.Value != nil {
		v, _ := json.Marshal(l.Value)
		m += string(v)
	}
	return m
}

func (l *Message) Deserialize(m string) error {
	parts := strings.Split(m, " ")
	if len(parts) < 1 {
		return fmt.Errorf("invalid message")
	}
	i := 0
	idx, err := strconv.ParseUint(parts[i], 10, 64)
	if err != nil {
		l.Idx = uint64(idx)
	}
	i += 1
	op, err := strconv.Atoi(parts[i])
	if err != nil {
		return err
	}
	l.Op = Op(op)
	i += 1
	if len(parts) > i {
		id, err := strconv.Atoi(parts[i])
		if err != nil {
			return err
		}
		i += 1
		gen, err := strconv.Atoi(parts[i])
		if err != nil {
			return err
		}
		l.Entity = uint64(id)<<32 | uint64(gen)
		i += 1
	}
	if l.Op == Add || l.Op == Set || l.Op == Remove {
		l.Key = parts[i]
		i += 1
	}
	if len(parts) > i {
		v := strings.Join(parts[i:], " ")
		err = json.Unmarshal([]byte(v), &l.Value)
		if err != nil {
			return err
		}
	}
	return nil
}

func (l Message) MarshalJSON() ([]byte, error) {
	// TODO: serialie idx
	m := fmt.Sprintf("%d ", l.Op)
	if l.Entity != 0 {
		id := uint32(l.Entity >> 32)
		gen := uint32(l.Entity & 0xFFFFFFFF)
		m += fmt.Sprintf("%d %d ", id, gen)
	}
	if l.Key != "" {
		m += l.Key + " "
	}
	if l.Value != nil {
		v, _ := json.Marshal(l.Value)
		m += string(v)
	}
	return json.Marshal(m)
}

func (l *Message) UnmarshalJSON(data []byte) error {
	var v string
	err := json.Unmarshal(data, &v)
	if err != nil {
		return err
	}
	parts := strings.Split(string(data[1:len(data)-1]), " ")
	if len(parts) < 1 {
		return fmt.Errorf("invalid message")
	}
	i := 0
	idx, err := strconv.Atoi(parts[i])
	if err != nil {
		l.Idx = uint64(idx)
	}
	i += 1
	op, err := strconv.Atoi(parts[i])
	if err != nil {
		return err
	}
	l.Op = Op(op)
	i += 1
	if len(parts) > i {
		id, err := strconv.Atoi(parts[i])
		if err != nil {
			return err
		}
		i += 1
		gen, err := strconv.Atoi(parts[i])
		if err != nil {
			return err
		}
		l.Entity = uint64(id)<<32 | uint64(gen)
		i += 1
	}
	if l.Op == Add || l.Op == Set || l.Op == Remove {
		l.Key = parts[i]
		i += 1
	}
	if len(parts) > i {
		v := strings.Join(parts[i:], " ")
		err = json.Unmarshal([]byte(v), &l.Value)
		if err != nil {
			return err
		}
	}
	return nil
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
		return fmt.Sprintf(`{"op":"add","path":"/%s/%d","value":%s}`, n, l.Entity, v)
	case Delete:
		return fmt.Sprintf(`{"op":"remove","path":":"/%s/%d"}`, n, l.Entity)
	case Add:
		v, err := json.Marshal(l.Value)
		if err != nil {
			return ""
		}
		return fmt.Sprintf(`{"op":"add","path":"/%s/%d/%s","value":%s}`, n, l.Entity, l.Key, v)
	case Set:
		v, err := json.Marshal(l.Value)
		if err != nil {
			return ""
		}
		return fmt.Sprintf(`{"op":"replace","path":"/%s/%d/%s","value":%s}`, n, l.Entity, l.Key, v)
	case Remove:
		return fmt.Sprintf(`{"op":"remove","path":"/%s/%d/%s"}`, n, l.Entity, l.Key)
	}
	return ""
}
