package modules

import (
	"github.com/SvenDH/recs/cluster"
)

type Sprite string

func RegisterSprite(s *cluster.Server) {
	cluster.RegisterComponent[Sprite](s, true, true, true)
}
