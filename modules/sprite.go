package modules

import (
	"github.com/SvenDH/recs/cluster"
)

type Sprite string

func RegisterSprite(s *cluster.Store) {
	cluster.RegisterComponent[Sprite](s, true, true, true)
}
