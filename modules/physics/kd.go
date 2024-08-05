package physics

import (
	"container/heap"

	"github.com/mlange-42/arche/ecs"
	"gonum.org/v1/gonum/spatial/kdtree"
)

type EntityLocation struct {
	Vec Vector2
	Heading float64
	Entity  ecs.Entity
}
func (p EntityLocation) Compare(c kdtree.Comparable, d kdtree.Dim) float64 {
	q := c.(EntityLocation)
	return p.Vec[int(d)] - q.Vec[int(d)]
}

func (p EntityLocation) Dims() int { return 2 }

func (p EntityLocation) Distance(c kdtree.Comparable) float64 {
	q := c.(EntityLocation)
	dx := q.Vec[0] - p.Vec[0]
	dy := q.Vec[1] - p.Vec[1]
	return dx*dx + dy*dy
}

type EntityLocations []EntityLocation

func (p EntityLocations) Index(i int) kdtree.Comparable { return p[i] }
func (p EntityLocations) Len() int { return len(p) }
func (p EntityLocations) Pivot(d kdtree.Dim) int {
	return plane{EntityLocations: p, Dim: d}.Pivot()
}
func (p EntityLocations) Slice(start, end int) kdtree.Interface { return p[start:end] }

// plane is a wrapping type that allows a Points type be pivoted on a dimension.
// The Pivot method of Plane uses MedianOfRandoms sampling at most 100 elements
// to find a pivot element.
type plane struct {
	kdtree.Dim
	EntityLocations
}

// randoms is the maximum number of random values to sample for calculation of
// median of random elements.
const randoms = 100

func (p plane) Less(i, j int) bool {
	return p.EntityLocations[i].Vec[int(p.Dim)] < p.EntityLocations[j].Vec[int(p.Dim)]
}

func (p plane) Pivot() int { return kdtree.Partition(p, kdtree.MedianOfRandoms(p, randoms)) }

func (p plane) Slice(start, end int) kdtree.SortSlicer {
	p.EntityLocations = p.EntityLocations[start:end]
	return p
}

func (p plane) Swap(i, j int) {
	p.EntityLocations[i], p.EntityLocations[j] = p.EntityLocations[j], p.EntityLocations[i]
}

type NDistKeeper struct {
	kdtree.Heap
}

func NewNDistKeeper(n int, d float64) *NDistKeeper {
	k := NDistKeeper{make(kdtree.Heap, 1, n)}
	k.Heap[0].Dist = d * d
	return &k
}

func (k *NDistKeeper) Keep(c kdtree.ComparableDist) {
	if c.Dist <= k.Heap[0].Dist {
		if len(k.Heap) == cap(k.Heap) {
			heap.Pop(k)
		}
		heap.Push(k, c)
	}
}
