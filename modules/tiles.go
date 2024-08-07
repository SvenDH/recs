package modules

import (
	"github.com/SvenDH/recs/cluster"
	"github.com/mlange-42/arche/ecs"
	"github.com/mlange-42/arche/generic"
)

type TileMap struct {
	Array []ecs.Entity
	Width  int
	Height int
	CellSize int
}

type Tile int64



type TileSystem struct {
	TileMap *TileMap
	filter *generic.Filter1[Tile]
	tilemapRes generic.Resource[TileMap]
}

func (s *TileSystem) Initialize(w *ecs.World) {
	ecs.AddResource(w, s.TileMap)
	s.filter = generic.NewFilter1[Tile]()
	s.tilemapRes = generic.NewResource[TileMap](w)

}

func (s *TileSystem) Update(w *ecs.World) {}

func (s *TileSystem) Finalize(w *ecs.World) {}

func RegisterTiles(s *cluster.Store) {
	cluster.RegisterComponent[Tile](s, true, true, true)
	cluster.RegisterSystem(s, &TileSystem{
		TileMap: &TileMap{
			Array: make([]ecs.Entity, 400 * 400),
			Width: 400,
			Height: 400,
			CellSize: 1,
		},
	})
}
