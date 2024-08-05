package physics

import (
	"math"

	"github.com/mlange-42/arche/ecs"
	"github.com/mlange-42/arche/generic"
	"github.com/SvenDH/recs/world"
)

const DegToRad = math.Pi / 180.0
const RadToDeg = 180.0 / math.Pi

func Clamp(v, low, high float64) float64 {
	if v < low {
		return low
	} else if v > high {
		return high
	}
	return v
}

type SysMoveBoids struct {
	Speed          float64
	MaxAcc         float64
	UpdateInterval int

	SeparationDist  float64
	SeparationAngle float64
	CohesionAngle   float64
	AlignmentAngle  float64

	AreaWidth  float64
	AreaHeight float64

	WallDist  float64
	WallAngle float64

	separationDistSq float64
	separationAngle  float64
	cohesionAngle    float64
	alignmentAngle   float64
	wallAngle        float64

	time   generic.Resource[world.Tick]
	filter generic.Filter4[Position, Heading, Neighbors, Rand256]
}

func (s *SysMoveBoids) Initialize(w *ecs.World) {
	s.time = generic.NewResource[world.Tick](w)
	s.filter = *generic.NewFilter4[Position, Heading, Neighbors, Rand256]()
	s.separationDistSq = s.SeparationDist * s.SeparationDist
	s.separationAngle = s.SeparationAngle * DegToRad
	s.cohesionAngle = s.CohesionAngle * DegToRad
	s.alignmentAngle = s.AlignmentAngle * DegToRad
	s.wallAngle = s.WallAngle * DegToRad
}

func (s *SysMoveBoids) Update(w *ecs.World) {
	v := ecs.GetResource[world.World](w)

	tick := s.time.Get().Tick
	modTick := uint8(tick % int64(s.UpdateInterval))
	query := s.filter.Query(w)
	for query.Next() {
		pos, head, neigh, r256 := query.Get()
		_ = neigh
		if uint8(*r256)%uint8(s.UpdateInterval) == modTick {
			h := float64(*head)
			turn := 0.0
			turn += Clamp(s.separation(*pos, h, neigh), -s.separationAngle, s.separationAngle)
			turn += Clamp(s.cohesion(*pos, h, neigh), -s.cohesionAngle, s.cohesionAngle)
			turn += Clamp(s.alignment(h, neigh), -s.separationAngle, s.separationAngle)
			av, avLen := s.wallAvoidance(*pos, h, s.AreaWidth, s.AreaHeight)
			turn += Clamp(av, -s.wallAngle*avLen, s.wallAngle*avLen)

			*head += Heading(turn)
		}
		d := head.Direction()
		pos[0] += d[0] * s.Speed
		pos[1] += d[1] * s.Speed

        v.Publish(query.Entity(), *pos)
	}
}

func (s *SysMoveBoids) Finalize(world *ecs.World) {}

func (s *SysMoveBoids) separation(pos Position, angle float64, neigh *Neighbors) float64 {
	if neigh.Count == 0 {
		return 0
	}
	n := &neigh.Entities[0].Vec
	distSq := n.DistanceSq(Vector2(pos))
	if distSq > s.separationDistSq {
		return 0
	}
	return -SubtractHeadings(math.Atan2(n[1]-pos[1], n[0]-pos[0]), angle)
}

func (s *SysMoveBoids) cohesion(pos Position, angle float64, neigh *Neighbors) float64 {
	if neigh.Count == 0 {
		return 0
	}
	cx, cy := 0.0, 0.0
	for i := 0; i < neigh.Count; i++ {
		n := &neigh.Entities[i].Vec
		cx += n[0]
		cy += n[1]
	}
	cx /= float64(neigh.Count)
	cy /= float64(neigh.Count)
	return SubtractHeadings(math.Atan2(cy-float64(pos[1]), cx-float64(pos[0])), angle)
}

func (s *SysMoveBoids) alignment(angle float64, neigh *Neighbors) float64 {
	if neigh.Count == 0 {
		return 0
	}
	dx, dy := 0.0, 0.0
	for i := 0; i < neigh.Count; i++ {
		n := float64(neigh.Entities[i].Heading)
		dx += math.Cos(n)
		dy += math.Sin(n)
	}
	dx /= float64(neigh.Count)
	dy /= float64(neigh.Count)
	return SubtractHeadings(math.Atan2(dy, dx), angle)
}

func (s *SysMoveBoids) wallAvoidance(pos Position, angle float64, w, h float64) (float64, float64) {
	target := Vector2{}
	if pos[0] < s.WallDist {
		target[0] += (s.WallDist - pos[0]) / s.WallDist
	}
	if pos[1] < s.WallDist {
		target[1] += (s.WallDist - pos[1]) / s.WallDist
	}
	if pos[0] > w-s.WallDist {
		target[0] -= (s.WallDist - (w - pos[0])) / s.WallDist
	}
	if pos[1] > h-s.WallDist {
		target[1] -= (s.WallDist - (h - pos[1])) / s.WallDist
	}
	if target[0] == 0 && target[1] == 0 {
		return 0, 0
	}
	return SubtractHeadings(target.Angle(), angle), target.Len()
}

func SubtractHeadings(h1, h2 float64) float64 {
	a360 := 2 * math.Pi
	if h1 < 0 || h1 >= a360 {
		h1 = math.Mod((math.Mod(h1, a360) + a360), a360)
	}
	if h2 < 0 || h2 >= a360 {
		h2 = math.Mod((math.Mod(h2, a360) + a360), a360)
	}
	diff := h1 - h2
	if diff > -math.Pi && diff <= math.Pi {
		return diff
	} else if diff > 0 {
		return diff - a360
	}
	return diff + a360
}