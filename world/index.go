package world

import (
	"encoding/json"
	"encoding/binary"

	"github.com/mlange-42/arche/ecs"
	"github.com/mlange-42/arche/generic"
	"github.com/tidwall/btree"
)

type EntityIndex struct {
	index   btree.Map[string, uint64]
	m 	 	map[ecs.Entity]string
	counter uint32
}

type EntitieIndexData struct {
	Index   []string `json:"index"`
	Ids     []uint64 `json:"ids"`
	Counter uint32   `json:"counter"`
}

func (i *EntityIndex) init() {
	i.m = make(map[ecs.Entity]string)
}

func (i *EntityIndex) Add(w *ecs.World, e ecs.Entity) string {
	if i.m == nil {i.init()}
	i.counter++
	idx := i.calcIndexOf(w, e, ecs.ComponentID[ChildOf](w))
	i.index.Set(idx, entityToId(e))
	i.m[e] = idx
	return idx
}

func (i *EntityIndex) Remove(w *ecs.World, e ecs.Entity) {
	if i.m == nil {i.init()}
	i.index.Delete(i.m[e])
	delete(i.m, e)
}

func (i *EntityIndex) Update(w *ecs.World, e ecs.Entity) string {
	if i.m == nil {i.init()}
	cid := ecs.ComponentID[ChildOf](w)
	newidx := i.calcIndexOf(w, e, cid)
	if i.m[e] != newidx {
		i.updateChildren(w, e, cid)
	}
	return newidx
}

func (i *EntityIndex) Iter() btree.MapIter[string, uint64] {
	return i.index.Iter()
}

func (i *EntityIndex) Len() int {
	return i.index.Len()
}

func (i *EntityIndex) MarshalJSON() ([]byte, error) {
	return json.Marshal(&EntitieIndexData{
		Index:   i.index.Keys(),
		Ids:     i.index.Values(),
		Counter: i.counter,
	})
}

func (u *EntityIndex) UnmarshalJSON(data []byte) error {
	aux := &EntitieIndexData{}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	u.counter = aux.Counter
	u.m = make(map[ecs.Entity]string)
	for i, idx := range aux.Index {
		u.index.Load(idx, aux.Ids[i])
		u.m[idToEntity(aux.Ids[i])] = idx
	}
	return nil
}

func (i *EntityIndex) updateChildren(w *ecs.World, e ecs.Entity, c ecs.ID) string {
	idx := i.m[e]
	newidx := i.calcIndexOf(w, e, c)
	i.index.Delete(idx)
	i.index.Set(newidx, entityToId(e))
	i.m[e] = newidx
	filter := generic.NewFilter1[ChildOf]().WithRelation(generic.T[ChildOf](), e)
	query := filter.Query(w)
	for query.Next() {
		i.updateChildren(w, query.Entity(), c)
	}
	return newidx
}

func (i *EntityIndex) calcIndexOf(w *ecs.World, e ecs.Entity, c ecs.ID) string {
	bs := make([]byte, 4)
    binary.BigEndian.PutUint32(bs, i.counter)
	idx := string(bs)
	if w.HasUnchecked(e, c) {
		parent := w.Relations().Get(e, c)
		if !parent.IsZero() {
			idx = i.m[parent]+idx
		}
	}
	return idx
}
