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
	"strings"
	"sync"
	"time"

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

type World struct {
	world ecs.World

	TPS                   float64
	Paused                bool
	ResetTerminationSteps bool
	CommitIndex           uint64

	wm        *WorldManager
	name      string
	ents      map[uint64]ecs.Entity
	comps     map[string]ecs.ID
	compNames map[ecs.ID]string
	log       []events.Message
	mu        sync.RWMutex
	changed   bool
	step      int64
	logFile   *os.File

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
}

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
	store      Store
	worldToName map[*ecs.World]string
	inmem       bool
	dir         string
	logger      *log.Logger
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
		store:                b,
		inmem:                 inmem,
		dir:                   dir,
		logger:                log.New(os.Stderr, "[world]: ", log.LstdFlags),
	}

	createListener := listener.NewCallback(
		func(w *ecs.World, ee ecs.EntityEvent) {
			w2 := ecs.GetResource[World](w)
			w2.addToLog(events.Message{Op: events.Create, Entity: entityToId(ee.Entity)})
		},
		event.EntityCreated,
	)
	deleteListener := listener.NewCallback(
		func(w *ecs.World, ee ecs.EntityEvent) {
			w2 := ecs.GetResource[World](w)
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
				t := wm.Components[cn]
				p := reflect.NewAt(t, w.GetUnchecked(ee.Entity, c)).Interface()
				d := reflect.ValueOf(p).Elem().Interface()
				w2.addToLog(events.Message{Op: events.Add, Entity: id, Key: cn, Value: d})
			}
		},
		event.ComponentAdded,
	)
	removeListener := listener.NewCallback(
		func(w *ecs.World, ee ecs.EntityEvent) {
			w2 := ecs.GetResource[World](w)
			id := entityToId(ee.Entity)
			if _, ok := w2.ents[id]; !ok {
				return
			}
			for _, c := range ee.RemovedIDs {
				w2.addToLog(events.Message{Op: events.Remove, Entity: id, Key: w2.compNames[c]})
			}
		},
		event.ComponentRemoved,
	)
	wm.listener = listener.NewDispatch(
		&createListener,
		&deleteListener,
		&addListener,
		&removeListener,
	)
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
		ents:      make(map[uint64]ecs.Entity),
		wm:        wm,
	}
	for _, c := range wm.Components {
		n := strings.ToLower(c.Name())
		id := ecs.TypeID(&w.world, c)
		w.comps[n] = id
		w.compNames[id] = n
	}
	wm.worlds[name] = w
	wm.worldToName[&w.world] = name

	w.rand = Rand{Source: rand.NewSource(int64(time.Now().UnixNano()))}
	w.time = Tick{}
	w.terminate = Termination{}
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
	// Each 100 seconds
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
	err := w.Load()
	if err != nil {
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
		logFile, err := os.OpenFile(filepath.Join(wm.dir, name+".log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			wm.logger.Printf("error opening log file: %s", err)
		} else {
			w.logFile = logFile
		}
	}
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
	w.Save(true)
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

func (w *World) idToEntity(id uint64) (ecs.Entity, error) {
	ent, ok := w.ents[id]
	if !ok {
		return ecs.Entity{}, fmt.Errorf("entity %d not found", id)
	}
	if !w.world.Alive(ent) {
		return ecs.Entity{}, fmt.Errorf("entity %d not found", id)
	}
	return ent, nil
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

func (w *World) Publish(e uint64, n string, d interface{}) {
	w.addToLog(events.Message{Op: events.Set, Entity: e, Key: n, Value: d})
}

func (w *World) Create(ctx context.Context, components map[string]interface{}) (uint64, error) {
	ecsComponents := make([]ecs.Component, 0)
	for k, v := range components {
		t, ok := w.comps[k]
		if ok {
			ecsComponents = append(ecsComponents, ecs.Component{ID: t, Comp: v})
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	e := w.world.NewEntityWith(ecsComponents...)
	id := entityToId(e)
	w.ents[id] = e
	return id, nil
}

func (w *World) Delete(ctx context.Context, e uint64) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	ent, err := w.idToEntity(e)
	if err != nil {
		return err
	}
	delete(w.ents, e)
	w.world.RemoveEntity(ent)
	return nil
}

func (w *World) Set(ctx context.Context, e uint64, n string, d interface{}) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	ent, err := w.idToEntity(e)
	if err != nil {
		return err
	}
	c, ok := w.comps[n]
	if !ok {
		return fmt.Errorf("component %s not found", n)
	}
	if !w.world.HasUnchecked(ent, c) {
		w.world.Assign(ent, ecs.Component{ID: c, Comp: d})
	} else {
		w.world.Set(ent, c, d)
	}
	w.Publish(e, n, d)
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
		ent, err := w.idToEntity(e)
		if err != nil {
			continue
		}
		components := make(map[string]interface{})
		for k, v := range w.comps {
			if w.world.HasUnchecked(ent, v) {
				components[k] = reflect.NewAt(w.wm.Components[k], w.world.GetUnchecked(ent, v)).Interface()
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
	ent, err := w.idToEntity(e)
	if err != nil {
		return 0, nil
	}
	t, ok := w.wm.Components[n]
	if !ok {
		return 0, nil
	}
	return w.CommitIndex, reflect.NewAt(t, w.world.GetUnchecked(ent, w.comps[n])).Interface()
}

func (w *World) GetAll(ctx context.Context, e uint64) (uint64, interface{}) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	ent, err := w.idToEntity(e)
	if err != nil {
		return 0, nil
	}
	res := make(map[string]interface{})
	for k, v := range w.comps {
		if w.world.HasUnchecked(ent, v) {
			res[k] = reflect.NewAt(w.wm.Components[k], w.world.GetUnchecked(ent, v)).Interface()
		}
	}
	return w.CommitIndex, res
}

func (w *World) List(ctx context.Context) (uint64, interface{}) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	res := make(map[uint64]map[string]interface{}, len(w.ents))
	for k := range w.ents {
		res[k] = make(map[string]interface{}, 0)
		for n, v := range w.comps {
			if w.world.HasUnchecked(w.ents[k], v) {
				res[k][n] = reflect.NewAt(w.wm.Components[n], w.world.GetUnchecked(w.ents[k], v)).Interface()
			}
		}
	}
	return w.CommitIndex, res
}

func (w *World) ListComponent(ctx context.Context, c string) (uint64, map[uint64]interface{}) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	t, ok := w.wm.Components[c]
	if !ok {
		return 0, nil
	}
	compid := w.comps[c]
	res := make(map[uint64]interface{}, len(w.ents))
	for id, k := range w.ents {
		res[id] = reflect.NewAt(t, w.world.GetUnchecked(k, compid)).Interface()
	}
	return w.CommitIndex, res
}

func (w *World) Iterate(ctx context.Context, e uint64, comp string, call func(uint64, uint64, interface{}) error) error {
	w.mu.RLock()
	defer w.mu.RUnlock()
	var ent ecs.Entity
	var err error
	if e != 0 {
		ent, err = w.idToEntity(e)
		if err != nil {
			return err
		}
	}
	idx := w.CommitIndex
	if comp != "" {
		c, ok := w.comps[comp]
		if !ok {
			return fmt.Errorf("component %s not found", comp)
		}
		if e != 0 {
			if w.world.HasUnchecked(ent, c) {
				if call(idx, e, reflect.NewAt(w.wm.Components[comp], w.world.GetUnchecked(ent, c)).Interface()) != nil {
					return nil
				}
			} else {
				return fmt.Errorf("entity %d does not have component %s", e, comp)
			}
		} else {
			for id, ent := range w.ents {
				if w.world.HasUnchecked(ent, c) {
					if call(idx, id, reflect.NewAt(w.wm.Components[comp], w.world.GetUnchecked(ent, c)).Interface()) != nil {
						return nil
					}
					idx = 0
				}
			}
		}
	} else {
		if e != 0 {
			res := make(map[string]interface{}, 0)
			for comp, id := range w.comps {
				if w.world.HasUnchecked(ent, id) {
					res[comp] = reflect.NewAt(w.wm.Components[comp], w.world.GetUnchecked(ent, id)).Interface()
				}
			}
			if call(idx, e, res) != nil {
				return nil
			}
		} else {
			for id, ent := range w.ents {
				res := make(map[string]interface{}, 0)
				for comp, compid := range w.comps {
					if w.world.HasUnchecked(ent, compid) {
						res[comp] = reflect.NewAt(w.wm.Components[comp], w.world.GetUnchecked(ent, compid)).Interface()
					}
				}
				if call(idx, id, res) != nil {
					return nil
				}
				idx = 0
			}
		}
	}
	return nil
}

func (w *World) Save(force bool) error {
	if w.wm.inmem || (!w.changed && !force) {
		return nil
	}
	// Save snapshot to temporary file
	path := filepath.Join(w.wm.dir, w.name+".json")
	dat, err := archeserde.Serialize(&w.world)
	if err != nil {
		return err
	}
	err = os.WriteFile(path+".temp", dat, 0644)
	if err != nil {
		return err
	}

	// Rename temporary file to actual file
	err = os.Rename(path+".temp", path)
	if err != nil {
		return err
	}

	// Clear log file
	if w.logFile != nil {
		w.logFile.Close()
		w.logFile, err = os.Create(filepath.Join(w.wm.dir, w.name+".log"))
		if err != nil {
			return err
		}
	}

	w.changed = false
	return nil
}

func (w *World) Load() error {
	if w.wm.inmem {
		return nil
	}
	path := filepath.Join(w.wm.dir, w.name+".json")
	dat, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	err = archeserde.Deserialize(dat, &w.world)
	if err != nil {
		return err
	}
	query := w.world.Query(ecs.All())
	for query.Next() {
		e := query.Entity()
		w.ents[entityToId(e)] = e
	}

	// Apply log
	logPath := filepath.Join(w.wm.dir, w.name+".log")
	logFile, err := os.Open(logPath)
	if err != nil {
		return err
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
			err = w.ApplyLog(&l)
			if err != nil {
				return err
			}
			w.CommitIndex = l.Idx
		}
	}

	return nil
}

func (w *World) ApplyLog(l *events.Message) error {
	var ent ecs.Entity
	var err error
	var comp ecs.ID
	var ok bool
	var d interface{}
	if l.Op != events.Create {
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
	}
	if l.Value != nil {
		v, _ := json.Marshal(l.Value)
		d = reflect.New(w.wm.Components[l.Key]).Interface()
		err = json.Unmarshal(v, d)
		if err != nil {
			return err
		}
	}
	switch l.Op {
	case events.Create:
		ent = w.world.NewEntity()
		w.ents[l.Entity] = ent
	case events.Delete:
		w.world.RemoveEntity(ent)
		delete(w.ents, l.Entity)
	case events.Add:
		w.world.Assign(ent, ecs.Component{ID: comp, Comp: d})
	case events.Set:
		w.world.Set(ent, comp, d)
	case events.Remove:
		w.world.Remove(ent, comp)
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
	w.rand = Rand{Source: rand.NewSource(int64(time.Now().UnixNano()))}
	ecs.AddResource(&w.world, &w.rand)
	w.time = Tick{}
	ecs.AddResource(&w.world, &w.time)
	w.terminate = Termination{}
	ecs.AddResource(&w.world, &w.terminate)
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
