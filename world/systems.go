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
}

func (s *FixedTermination) Initialize(w *ecs.World) {
	s.termRes = generic.NewResource[Termination](w)
}

func (s *FixedTermination) Update(w *ecs.World) {
	term := s.termRes.Get()
	w2 := ecs.GetResource[World](w)
	if w2.step+1 >= s.Steps {
		term.Terminate = true
	}
	w2.step++
}

func (s *FixedTermination) Finalize(w *ecs.World) {}

type PersistSystem struct {
	UpdateInterval int64
}

func (s *PersistSystem) Initialize(w *ecs.World) {}

func (s *PersistSystem) Update(w *ecs.World) {
	if ecs.GetResource[Tick](w).Tick%s.UpdateInterval == 0 {
		ecs.GetResource[World](w).Save(false)
	}
}

func (s *PersistSystem) Finalize(w *ecs.World) {}

type EventPublisher struct {
	FlushInterval int64
	broker        Store
}

func (ep *EventPublisher) Initialize(w *ecs.World) {}

func (ep *EventPublisher) Update(w *ecs.World) {
	if ecs.GetResource[Tick](w).Tick%ep.FlushInterval == 0 {
		w2 := ecs.GetResource[World](w)

		if len(w2.log) > 0 {
			// TODO: possibly use world creation context here
			ctx := context.TODO()
			ep.broker.Publish(ctx, w2.name, w2.log)
			w2.log = w2.log[:0]
		}
	}
}

func (ep *EventPublisher) Finalize(w *ecs.World) {}
