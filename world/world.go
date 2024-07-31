package world

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/SvenDH/recs/events"
	archeserde "github.com/mlange-42/arche-serde"
	"github.com/mlange-42/arche/ecs"
	"github.com/mlange-42/arche/ecs/event"
	"github.com/mlange-42/arche/generic"
	"github.com/mlange-42/arche/listener"
)

type Store interface {
	Publish(ctx context.Context, topic string, messages []events.Message) error
	Create(ctx context.Context, world string, components map[string]interface{}) (uint64, error)
}

type ChildOf struct {
	ecs.Relation
}

type Parent uint64

type WorldManager struct {
	worlds                map[string]*World
	Components            map[string]reflect.Type
	Systems               []System
	TPS                   float64
	Steps                 int64
	ResetTerminationSteps bool
	Wal                   bool

	mu          sync.RWMutex
	listener    listener.Dispatch
	store       Store
	worldToName map[*ecs.World]string
	inmem       bool
	dir         string
	logger      *log.Logger
	newlineRe   *regexp.Regexp
}

type World struct {
	world ecs.World

	TPS                   float64
	Paused                bool
	ResetTerminationSteps bool
	CommitIndex           uint64
	MaxIdx                uint32

	wm        *WorldManager
	name      string
	comps     map[string]ecs.ID
	compNames map[ecs.ID]string
	index     EntityIndex
	log       []events.Message
	mu        sync.RWMutex
	changed   bool
	step      int64
	logFile   *os.File
	parentID ecs.ID

	rand        Rand
	time        Tick
	terminate   Termination
	systems     []System
	toRemove    []System
	nextDraw    time.Time
	nextUpdate  time.Time
	initialized bool
	locked      bool
	tickRes     generic.Resource[Tick]
	termRes     generic.Resource[Termination]
	indexRes    generic.Resource[EntityIndex]
}

func NewWorldManager(inmem bool, dir string, wal bool, b Store) *WorldManager {
	if dir != "" && !inmem {
		if err := os.MkdirAll(dir, os.ModePerm); err != nil {
			panic(err)
		}
	}
	wm := &WorldManager{
		worlds:                make(map[string]*World, 0),
		Components:            make(map[string]reflect.Type, 0),
		Systems:               make([]System, 0),
		Steps:                 300,
		ResetTerminationSteps: true,
		Wal:                   wal,
		worldToName:           make(map[*ecs.World]string, 0),
		store:                 b,
		inmem:                 inmem,
		dir:                   dir,
		logger:                log.New(os.Stderr, "[world]: ", log.LstdFlags),
		newlineRe:             regexp.MustCompile(`\n`),
	}

	createListener := listener.NewCallback(
		func(w *ecs.World, ee ecs.EntityEvent) {
			w2 := ecs.GetResource[World](w)
			if ee.Entity.ID() > w2.MaxIdx {
				w2.MaxIdx = ee.Entity.ID()
			}
			w2.index.Add(w, ee.Entity)
			w2.addToLog(events.Message{Op: events.Create, Entity: entityToId(ee.Entity)})
		},
		event.EntityCreated,
	)
	deleteListener := listener.NewCallback(
		func(w *ecs.World, ee ecs.EntityEvent) {
			w2 := ecs.GetResource[World](w)
			w2.index.Remove(w, ee.Entity)
			w2.addToLog(events.Message{Op: events.Delete, Entity: entityToId(ee.Entity)})
		},
		event.EntityRemoved,
	)
	addListener := listener.NewCallback(
		func(w *ecs.World, ee ecs.EntityEvent) {
			w2 := ecs.GetResource[World](w)
			id := entityToId(ee.Entity)
			for _, c := range ee.AddedIDs {
				cn := w2.compNames[c]
				var d interface{}
				if c == w2.parentID {
					p := w.Relations().Get(ee.Entity, w2.parentID)
					d = Parent(entityToId(p))
				} else {
					t := wm.Components[cn]
					p := reflect.NewAt(t, w.GetUnchecked(ee.Entity, c)).Interface()
					d = reflect.ValueOf(p).Interface()
				}
				w2.addToLog(events.Message{Op: events.Add, Entity: id, Key: cn, Value: d})
			}
		},
		event.ComponentAdded,
	)
	removeListener := listener.NewCallback(
		func(w *ecs.World, ee ecs.EntityEvent) {
			w2 := ecs.GetResource[World](w)
			id := entityToId(ee.Entity)
			for _, c := range ee.RemovedIDs {
				w2.addToLog(events.Message{Op: events.Remove, Entity: id, Key: w2.compNames[c]})
			}
		},
		event.ComponentRemoved,
	)
	relationListener := listener.NewCallback(
		func(w *ecs.World, ee ecs.EntityEvent) {
			w2 := ecs.GetResource[World](w)
			if ee.NewRelation != nil || ee.OldRelation != nil {
				w2.index.Update(w, ee.Entity)
			}
		},
		event.RelationChanged,
	)

	wm.listener = listener.NewDispatch(
		&createListener,
		&deleteListener,
		&addListener,
		&removeListener,
		&relationListener,
	)

	wm.Components["parent"] = reflect.TypeOf(Parent(0))

	return wm
}

func (wm *WorldManager) New(ctx context.Context, name string) *World {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	w := &World{
		world:     ecs.NewWorld(),
		TPS:       wm.TPS,
		name:      name,
		comps:     make(map[string]ecs.ID),
		compNames: make(map[ecs.ID]string),
		log:       make([]events.Message, 0),
		index:     EntityIndex{},
		wm:        wm,
		rand:      Rand{Source: rand.NewSource(int64(time.Now().UnixNano()))},
		time:      Tick{},
		terminate: Termination{},
	}
	for _, c := range wm.Components {
		n := strings.ToLower(c.Name())
		var id ecs.ID
		if n == "parent" {
			id = ecs.ComponentID[ChildOf](&w.world)
		} else {
			id = ecs.TypeID(&w.world, c)
		}
		w.comps[n] = id
		w.compNames[id] = n
	}
	w.parentID = ecs.ComponentID[ChildOf](&w.world)

	ecs.AddResource(&w.world, &w.index)
	ecs.AddResource(&w.world, &w.rand)
	ecs.AddResource(&w.world, &w.time)
	ecs.AddResource(&w.world, &w.terminate)
	ecs.AddResource(&w.world, w)

	for _, s := range wm.Systems {
		w.AddSystem(s)
	}
	// At each step
	w.AddSystem(&EventPublisher{
		broker:        wm.store,
		FlushInterval: 1,
	})
	// Each 10 seconds
	u := 300
	if w.TPS > 0 {
		u = int(w.TPS) * 10
	}
	w.AddSystem(&PersistSystem{
		UpdateInterval: int64(u),
	})
	// Terminate after `Steps` steps
	w.AddSystem(&FixedTermination{
		Steps: wm.Steps,
	})
	if err := w.Load(); err != nil {
		wm.logger.Printf("error loading world %s: %s", name, err)
	}

	w.terminate.Terminate = false
	w.ResetTerminationSteps = wm.ResetTerminationSteps
	w.TPS = wm.TPS

	w.world.SetListener(&wm.listener)

	if wm.Wal && !wm.inmem {
		if wm.dir == "" {
			panic("WAL requires a directory to store logs")
		}
		logFile, err := os.OpenFile(filepath.Join(wm.dir, name), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			wm.logger.Printf("error opening log file: %s", err)
		} else {
			w.logFile = logFile
		}
	}

	wm.worlds[name] = w
	wm.worldToName[&w.world] = name

	w.initialize()

	go w.Run(ctx)

	return w
}

func (wm *WorldManager) Delete(name string) {
	wm.mu.Lock()
	defer wm.mu.Unlock()
	w, ok := wm.worlds[name]
	if !ok {
		return
	}
	w.logFile.Close()
	w.logFile = nil
	w.terminate.Terminate = true
	delete(wm.worlds, name)
	delete(wm.worldToName, &w.world)
}

func (wm *WorldManager) Get(ctx context.Context, name string) *World {
	wm.mu.RLock()
	defer wm.mu.RUnlock()
	w, ok := wm.worlds[name]
	if !ok {
		return nil
	}
	return w
}

func (w *World) New(ctx context.Context, components ...interface{}) (uint64, error) {
	ecsComponents := make([]ecs.Component, 0)
	parents := []ecs.Entity{}
	for _, v := range components {
		t := reflect.TypeOf(v).Elem()
		cid, ok := w.comps[strings.ToLower(t.Name())]
		if ok {
			if cid == w.parentID {
				newParent, err := w.idToEntity(uint64(*(v.(*Parent))))
				if err != nil {
					return 0, err
				}
				parents = append(parents, newParent)
				v = &ChildOf{}
			}
			ecsComponents = append(ecsComponents, ecs.Component{ID: cid, Comp: v})
		}
	}
	builder := ecs.NewBuilderWith(&w.world, ecsComponents...)
	if len(parents) > 0 {
		builder = builder.WithRelation(w.parentID)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return entityToId(builder.New(parents...)), nil
}

func (w *World) Delete(ctx context.Context, e uint64) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	ent, err := w.idToEntity(e)
	if err != nil {
		return err
	}
	w.world.RemoveEntity(ent)
	return nil
}

func (w *World) Set(ctx context.Context, e uint64, values ...interface{}) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	ent, err := w.idToEntity(e)
	if err != nil {
		return err
	}
	for _, v := range values {
		t := reflect.TypeOf(v)
		n := strings.ToLower(t.Name())
		c, ok := w.comps[n]
		if !ok {
			continue
		}
		if c == w.parentID {
			if err = w.setParent(e, uint64(v.(Parent))); err != nil {
				return err
			}
		} else if !w.world.HasUnchecked(ent, c) {
			w.world.Assign(ent, ecs.Component{ID: c, Comp: v})
		} else {
			w.world.Set(ent, c, v)
		}
		w.setPublish(e, n, v)
	}
	return nil
}

func (w *World) Remove(ctx context.Context, e uint64, n string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	ent, err := w.idToEntity(e)
	if err != nil {
		return err
	}
	t, ok := w.comps[n]
	if !ok {
		return fmt.Errorf("component %s not found", n)
	}
	if !w.world.HasUnchecked(ent, t) {
		return fmt.Errorf("entity %d does not have component %s", e, n)
	}
	w.world.Remove(ent, t)
	return nil
}

func (w *World) Move(ctx context.Context, to string, copy bool, entities ...uint64) ([]uint64, error) {
	if to == w.name {
		return nil, fmt.Errorf("can't move to the same world")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	// TODO: make move reliable somehow (rollback on error)
	newEntities := make([]uint64, 0, len(entities))
	for _, e := range entities {
		_, err := w.idToEntity(e)
		if err != nil {
			continue
		}
		components := make(map[string]interface{})
		for k, v := range w.comps {
			if c := w.getComp(e, k, v); c != nil {
				components[k] = c
			}
		}
		new, err := w.wm.store.Create(ctx, to, components)
		if err != nil {
			continue
		}
		if !copy {
			w.Delete(ctx, e)
		}
		newEntities = append(newEntities, new)
	}
	return newEntities, nil

}

func (w *World) Get(ctx context.Context, e uint64, n string) (uint64, interface{}) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	_, err := w.idToEntity(e)
	if err != nil {
		return 0, err
	}
	comps, err := w.checkComponents(n)
	if err != nil {
		return 0, err
	}
	res := map[string]interface{}{"id": e}
	for k, v := range comps {
		if c := w.getComp(e, k, v); c != nil {
			res[k] = c
		}
	}
	return w.CommitIndex, res

}

func (w *World) Iter(
	ctx context.Context,
	n string,
	call func(uint64, uint64, interface{}) error,
) error {
	w.mu.RLock()
	defer w.mu.RUnlock()
	idx := w.CommitIndex
	comps, err := w.checkComponents(n)
	if err != nil {
		return err
	}
	iter := w.index.Iter()
	for ok := iter.First(); ok; ok = iter.Next() {
		id := iter.Value()
		res := map[string]interface{}{"id": id}
		for comp, compid := range comps {
			if c := w.getComp(id, comp, compid); c != nil {
				res[comp] = c
			}
		}
		if call(idx, id, res) != nil {
			return nil
		}
		idx = 0
	}
	return nil
}

func (w *World) checkComponents(components string) (map[string]ecs.ID, error) {
	comps := w.comps
	if components != "" {
		comps = map[string]ecs.ID{}
		for _, s := range strings.Split(components, ",") {
			c, ok := w.comps[s]
			if !ok {
				return nil, fmt.Errorf("component %s not found", s)
			}
			comps[s] = c
		}
	}
	return comps, nil
}

func (w *World) addToLog(l events.Message) {
	l.Idx = w.CommitIndex + 1
	w.log = append(w.log, l)
	w.CommitIndex += 1
	w.changed = true
	if w.ResetTerminationSteps {
		w.step = 0
	}
	if w.logFile != nil {
		s, _ := json.Marshal(l)
		_, err := w.logFile.WriteString(string(s) + "\n")
		if err != nil {
			log.Printf("error writing to log file: %s", err)
		}
	}
}

func (w *World) setPublish(e uint64, n string, d interface{}) {
	w.addToLog(events.Message{Op: events.Set, Entity: e, Key: n, Value: d})
}

func (w *World) idToEntity(id uint64) (ecs.Entity, error) {
	ent := idToEntity(id)
	if ent.ID() > w.MaxIdx || !w.world.Alive(ent) {
		return ecs.Entity{}, fmt.Errorf("entity %d not found", id)
	}
	return ent, nil
}

func (w *World) getComp(e uint64, n string, c ecs.ID) interface{} {
	ent := idToEntity(e)
	if w.world.HasUnchecked(ent, c) {
		if c == w.parentID {
			return Parent(entityToId(w.world.Relations().Get(ent, w.parentID)))
		} else {
			t := w.wm.Components[n]
			return reflect.NewAt(t, w.world.GetUnchecked(ent, c)).Interface()
		}
	}
	return nil
}

func (w *World) setParent(c uint64, p uint64) error {
	child := idToEntity(c)
	if p == 0 {
		if w.world.HasUnchecked(child, w.parentID) {
			w.world.Remove(child, w.parentID)
		}
		return nil
	}
	parent, err := w.idToEntity(p)
	if err != nil {
		return err
	}
	if w.world.HasUnchecked(child, w.parentID) {
		w.world.Relations().Set(child, w.parentID, parent)
	} else {
		w.world.Relations().Exchange(child, []ecs.ID{w.parentID}, nil, w.parentID, parent)
	}
	return nil
}

func (w *World) TryCompact() error {
	if w.wm.inmem || !w.changed {
		return nil
	}
	// Serialize world to snapshot record
	dat, err := archeserde.Serialize(&w.world)
	if err != nil {
		return err
	}
	newDat := w.wm.newlineRe.ReplaceAllString(string(dat), "")
	dat, err = json.Marshal(events.Message{
		Idx:   w.CommitIndex,
		Op:    events.Snapshot,
		Value: newDat,
	})
	if err != nil {
		return err
	}
	// Save snapshot to temporary file
	path := filepath.Join(w.wm.dir, w.name)
	err = os.WriteFile(path+".tmp", []byte(string(dat)+"\n"), 0644)
	if err != nil {
		return err
	}
	if w.logFile != nil {
		w.logFile.Close()
	}
	// Rename temporary file to actual file
	err = os.Rename(path+".tmp", path)
	if err != nil {
		return err
	}
	w.logFile, err = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	w.changed = false
	return nil
}

func (w *World) Load() error {
	if w.wm.inmem {
		return nil
	}
	// Apply log
	logPath := filepath.Join(w.wm.dir, w.name)
	logFile, err := os.Open(logPath)
	if err != nil {
		return nil
	}
	defer logFile.Close()
	scanner := bufio.NewScanner(logFile)
	for scanner.Scan() {
		var l events.Message
		err := json.Unmarshal(scanner.Bytes(), &l)
		if err != nil {
			return err
		}
		if l.Idx > w.CommitIndex {
			err = w.applyLog(&l)
			if err != nil {
				return err
			}
			w.CommitIndex = l.Idx
		}
	}
	return nil
}

func (w *World) applyLog(l *events.Message) error {
	var ent ecs.Entity
	var err error
	var comp ecs.ID
	var ok bool
	var d interface{}
	if l.Op != events.Create && l.Op != events.Snapshot {
		ent, err = w.idToEntity(l.Entity)
		if err != nil {
			return err
		}
	}
	if l.Key != "" {
		comp, ok = w.comps[l.Key]
		if !ok {
			return fmt.Errorf("component %s not found", l.Key)
		}
		if l.Value != nil {
			v, _ := json.Marshal(l.Value)
			d = reflect.New(w.wm.Components[l.Key]).Interface()
			err = json.Unmarshal(v, d)
			if err != nil {
				return err
			}
		}
	}
	switch l.Op {
	case events.Create:
		ent = w.world.NewEntity()
		w.index.Add(&w.world, ent)
	case events.Delete:
		w.world.RemoveEntity(ent)
		w.index.Remove(&w.world, ent)
	case events.Add:
		w.world.Assign(ent, ecs.Component{ID: comp, Comp: d})
	case events.Set:
		w.world.Set(ent, comp, d)
	case events.Remove:
		w.world.Remove(ent, comp)
	case events.Snapshot:
		return archeserde.Deserialize([]byte(l.Value.(string)), &w.world)
	}
	return nil
}

func (w *World) AddSystem(sys System) {
	if w.initialized {
		panic("adding systems after model initialization is not implemented yet")
	}
	w.systems = append(w.systems, sys)
}

func (w *World) RemoveSystem(sys System) {
	w.toRemove = append(w.toRemove, sys)
	if !w.locked {
		w.removeSystems()
	}
}

func (w *World) removeSystems() {
	rem := w.toRemove
	w.toRemove = w.toRemove[:0]
	for _, sys := range rem {
		w.removeSystem(sys)
	}
}

func (w *World) removeSystem(sys System) {
	if w.locked {
		panic("can't remove a system in locked state")
	}
	idx := -1
	for i := 0; i < len(w.systems); i++ {
		if sys == w.systems[i] {
			idx = i
			break
		}
	}
	if idx < 0 {
		panic(fmt.Sprintf("can't remove system %T: not in the world", sys))
	}
	w.systems[idx].Finalize(&w.world)
	w.systems = append(w.systems[:idx], w.systems[idx+1:]...)
}

func (w *World) initialize() {
	if w.initialized {
		panic("world is already initialized")
	}
	if w.TPS == 0 {
		w.TPS = 30
	}
	w.tickRes = generic.NewResource[Tick](&w.world)
	w.termRes = generic.NewResource[Termination](&w.world)
	w.indexRes = generic.NewResource[EntityIndex](&w.world)
	w.locked = true
	for _, sys := range w.systems {
		sys.Initialize(&w.world)
	}
	w.locked = false
	w.removeSystems()
	w.initialized = true
	w.nextDraw = time.Time{}
	w.nextUpdate = time.Time{}
	w.tickRes.Get().Tick = 0
}

func (w *World) update() bool {
	if !w.initialized {
		panic("the world is not initialized")
	}
	if w.Paused {
		return true
	}
	w.locked = true
	w.mu.Lock()
	update := w.updateSystemsTimed()
	w.mu.Unlock()
	w.locked = false

	w.removeSystems()

	if update {
		time := w.tickRes.Get()
		time.Tick++
	} else {
		nextUpdate := w.nextUpdate
		if (w.Paused || w.TPS > 0) && w.nextDraw.Before(nextUpdate) {
			nextUpdate = w.nextDraw
		}
		t := time.Now()
		wait := nextUpdate.Sub(t)
		if wait > 0 {
			time.Sleep(wait)
		}
	}
	time := w.tickRes.Get()
	time.Tick++
	return !w.termRes.Get().Terminate
}

func (w *World) updateSystemsTimed() bool {
	update := false
	if w.Paused {
		update = !time.Now().Before(w.nextUpdate)
		if update {
			tps := limitedFps(w.TPS, 10)
			w.nextUpdate = nextTime(w.nextUpdate, tps)
		}
		return false
	}
	if w.TPS <= 0 {
		update = true
		w.updateSystems()
	} else {
		update = !time.Now().Before(w.nextUpdate)
		if update {
			w.nextUpdate = nextTime(w.nextUpdate, w.TPS)
			w.updateSystems()
		}
	}
	return update
}

func (w *World) updateSystems() bool {
	for _, sys := range w.systems {
		sys.Update(&w.world)
	}
	return true
}

func (w *World) finalize() {
	w.locked = true
	for _, sys := range w.systems {
		sys.Finalize(&w.world)
	}
	w.locked = false
	w.removeSystems()
	w.wm.Delete(w.name)
}

func (w *World) Run(ctx context.Context) {
	if !w.initialized {
		w.initialize()
	}
	w.wm.logger.Printf("World %s started", w.name)
	for w.update() {
	}
	w.finalize()
	w.wm.logger.Printf("World %s terminated", w.name)
}

func (w *World) Reset() {
	w.world.Reset()
	w.systems = []System{}
	w.toRemove = w.toRemove[:0]
	w.nextDraw = time.Time{}
	w.nextUpdate = time.Time{}
	w.initialized = false
	w.tickRes = generic.Resource[Tick]{}
	w.indexRes = generic.Resource[EntityIndex]{}
	w.rand = Rand{Source: rand.NewSource(int64(time.Now().UnixNano()))}
	w.time = Tick{}
	w.terminate = Termination{}
	ecs.AddResource(&w.world, &w.rand)
	ecs.AddResource(&w.world, &w.time)
	ecs.AddResource(&w.world, &w.terminate)
	ecs.AddResource(&w.world, &w.index)
	ecs.AddResource(&w.world, &w)
}

func limitedFps(actual, target float64) float64 {
	if actual > target || actual <= 0 {
		return target
	}
	return actual
}

func nextTime(last time.Time, fps float64) time.Time {
	if fps <= 0 {
		return last
	}
	dt := time.Second / time.Duration(fps)
	now := time.Now()
	if now.After(last.Add(200 * time.Millisecond)) {
		return now.Add(-10 * time.Millisecond)
	}
	return last.Add(dt)
}

func entityToId(e ecs.Entity) uint64 {
	return uint64(e.ID())<<32 | uint64(e.Generation())
}

func idToEntity(id uint64) ecs.Entity {
	// Cheat private fields because type casting uint64 to ecs.Entity is not possible
	ent := ecs.Entity{}
	p := reflect.ValueOf(&ent)
	val := reflect.Indirect(p)
	realPtrToId := (*uint32)(unsafe.Pointer(val.FieldByName("id").UnsafeAddr()))
	realPtrToGen := (*uint32)(unsafe.Pointer(val.FieldByName("gen").UnsafeAddr()))
	*realPtrToId = uint32(id >> 32)
	*realPtrToGen = uint32(id & 0xFFFFFFFF)
	return ent
}
