# RECS
Distributed Entity Component System for Go

Proof-of-concept implementation of a distributed [Entity Component System](https://en.wikipedia.org/wiki/Entity_component_system) in [Go](https://go.dev/). This project is a work in progress and is not intended for production use. And entity component system is a design pattern that is used to separate data from logic in a game engine. This allows for a more modular and flexible design such as add components to entities at runtime. This project is a distributed implementation of an entity component system. This means that the entities and components in different "worlds" are ran on different nodes in a network. This allows for a more scalable design where the entities and components can be distributed over multiple nodes in a network.

## Installation
```bash
go get github.com/SvenDH/recs
```

## Usage
For now there is a default world with moving boids that is rendered in a web browser. To run the example use the following command in the root of the project
```bash
go run . data
```

Add a empty entity to a world to create the world
```bash
curl -XPOST localhost:8080/world -d '{}'
```

Now you can navigate to http://localhost:8080 in your browser to view the world

## Features
- [x] Add entities and components to a world (uses [Arche](https://github.com/mlange-42/arche) as ECS implementation)
- [x] Distributed worlds (uses [Hashicorp's](https://github.com/hashicorp/raft) Raft implementation)
- [x] Log/WAL based world persistence
- [x] SSE based real-time world updates
- [ ] Entity indexing for different queries (e.g. get all entities within a certain radius)
- [ ] Web based world viewer and editor
- [ ] Make editing and viewing worlds and entities permission based
- [ ] Add tests
