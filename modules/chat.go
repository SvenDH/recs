package modules

import (
	"log"

	"github.com/SvenDH/recs/store"
	"github.com/mlange-42/arche/ecs"
	"github.com/mlange-42/arche/generic"
)

type Message string

type Chat struct {
	filter      generic.Filter1[Message]
}

func (c *Chat) Initialize(w *ecs.World) {
	c.filter = *generic.NewFilter1[Message]()
}

func (c *Chat) Update(w *ecs.World) {
	query := c.filter.Query(w)

	for query.Next() {
		msg := query.Get()
		log.Println(msg)
	}
}

func (c *Chat) Finalize(w *ecs.World) {}

func RegisterChat(s *store.Store) {
	store.RegisterComponent[Message](s)
	//store.RegisterSystem(s, &Chat{})
}


