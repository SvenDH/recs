package modules

import (
	"github.com/SvenDH/recs/cluster"
)

type Name string
type Owner string

func RegisterBase(s *cluster.Server) {
	cluster.RegisterComponent[Name](s)
	cluster.RegisterComponent[Owner](s)
}
