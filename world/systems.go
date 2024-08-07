package world

import (
	"context"

	"github.com/mlange-42/arche/ecs"
	"github.com/mlange-42/arche/generic"
)

type System interface {
	Initialize(w *ecs.World)
	Update(w *ecs.World)
	Finalize(w *ecs.World)
}

type FixedTermination struct {
	Steps   int64
	termRes generic.Resource[Termination]
	world   *World
}

func (s *FixedTermination) Initialize(w *ecs.World) {
	s.world = ecs.GetResource[World](w)
	s.termRes = generic.NewResource[Termination](w)
}

func (s *FixedTermination) Update(w *ecs.World) {
	term := s.termRes.Get()
	if s.world.step+1 >= s.Steps {
		term.Terminate = true
	}
	s.world.step++
}

func (s *FixedTermination) Finalize(w *ecs.World) {}

type PersistSystem struct {
	UpdateInterval int64
	world          *World
	tick           *Tick
}

func (s *PersistSystem) Initialize(w *ecs.World) {
	s.world = ecs.GetResource[World](w)
	s.tick = ecs.GetResource[Tick](w)
}

func (s *PersistSystem) Update(w *ecs.World) {
	if s.tick.Tick%s.UpdateInterval == 0 {
		if err := s.world.TryCompact(); err != nil {
			s.world.wm.logger.Printf("Failed to compact world: %s", err.Error())
		}
	}
}

func (s *PersistSystem) Finalize(w *ecs.World) {}

type EventPublisher struct {
	FlushInterval int64
	broker        Store
	world         *World
	tick          *Tick
}

func (ep *EventPublisher) Initialize(w *ecs.World) {
	ep.world = ecs.GetResource[World](w)
	ep.tick = ecs.GetResource[Tick](w)
}

func (ep *EventPublisher) Update(w *ecs.World) {
	if ep.tick.Tick%ep.FlushInterval == 0 {
		if len(ep.world.log) > 0 {
			// TODO: possibly use world creation context here
			ctx := context.TODO()
			ep.broker.Publish(ctx, ep.world.name, ep.world.log)
			ep.world.log = ep.world.log[:0]
		}
	}
}

func (ep *EventPublisher) Finalize(w *ecs.World) {}
