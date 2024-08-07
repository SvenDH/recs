package physics

import (
	"math"

	"github.com/SvenDH/recs/cluster"
	"github.com/SvenDH/recs/world"
	"github.com/mlange-42/arche/ecs"
	"github.com/mlange-42/arche/generic"
)

const MaxNeighbors = 8

type Position Vector2

type Velocity Vector2

type Heading float64

func (h *Heading) Direction() Vector2 {
	return Vector2{math.Cos(float64(*h)), math.Sin(float64(*h))}
}

func (h *Heading) Wrap() {
	*h = Heading(math.Mod(float64(*h), 2*math.Pi))
	if *h < 0 {
		*h = Heading(2*math.Pi - float64(*h))
	}
}

type Rand256 uint8

type PhysicsSystem struct {
	filter *generic.Filter2[Position, Velocity]
}

func (s *PhysicsSystem) Initialize(w *ecs.World) {
	s.filter = generic.NewFilter2[Position, Velocity]()
}

func (s *PhysicsSystem) Update(w *ecs.World) {
	v := ecs.GetResource[world.World](w)
	query := s.filter.Query(w)
	for query.Next() {
		pos, vel := query.Get()
		if vel[0] == 0 && vel[1] == 0 {
			continue
		}
		pos[0] += vel[0]
		pos[1] += vel[1]

		v.Publish(query.Entity(), *pos)
	}
}

func (s *PhysicsSystem) Finalize(w *ecs.World) {}

func RegisterPhysics(s *cluster.Store) {
	cluster.RegisterComponent[Position](s, true, true, true)
	cluster.RegisterComponent[Velocity](s, true, true, true)
	cluster.RegisterComponent[Heading](s, true, true, true)
	cluster.RegisterComponent[Neighbors](s, false, false, true)
	cluster.RegisterComponent[Rand256](s, false, true, true)
	cluster.RegisterSystem(
		s,
		&SysInitBoids{1000},
		&PhysicsSystem{},
		&SysNeighbors{
			Neighbors: 8,
			Radius:    50,
			BuildStep: 4,
		},
		&SysMoveBoids{
			Speed:           1,
			UpdateInterval:  4,
			SeparationDist:  8,
			SeparationAngle: 3,
			CohesionAngle:   1.5,
			AlignmentAngle:  2,
			WallDist:        80,
			WallAngle:       12,
			AreaWidth:       800,
			AreaHeight:      800,
		},
	)
}
