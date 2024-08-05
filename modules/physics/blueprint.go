package physics

import (
	"math"
	"math/rand"

	"github.com/SvenDH/recs/modules"
	"github.com/mlange-42/arche/ecs"
	"github.com/mlange-42/arche/generic"
)

type SysInitBoids struct {
	Count int
}

func (s *SysInitBoids) Initialize(w *ecs.World) {
	scr := Area{400, 400}
	ecs.AddResource(w, &scr)
	//area := generic.NewResource[Area](w)
	//scr := area.Get()

	builder := generic.NewMap5[modules.Sprite, Position, Heading, Neighbors, Rand256](w)
	query := builder.NewBatchQ(s.Count)
	for query.Next() {
		sprite, pos, head, _, r256 := query.Get()
		*sprite = modules.Sprite("boid")
		pos[0] = rand.Float64()*float64(scr[0])
		pos[1] = rand.Float64()*float64(scr[1])
		*head = Heading(rand.Float64() * math.Pi * 2)
		*r256 = Rand256(rand.Int31n(256))
	}
}

func (s *SysInitBoids) Update(w *ecs.World) {}

func (s *SysInitBoids) Finalize(w *ecs.World) {}