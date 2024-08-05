package physics

import (
	"math"
)

type Vector2 [2]float64

type Area Vector2

func (v Vector2) DistanceSq(other Vector2) float64 {
	dx := v[0] - other[0]
	dy := v[1] - other[1]
	return dx*dx + dy*dy
}

func (v Vector2) Len() float64 {
	return math.Hypot(v[0], v[1])
}

func (v Vector2) Angle() float64 {
	return math.Atan2(v[1], v[0])
}