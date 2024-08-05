import * as THREE from 'three';

class WorldState
{
    constructor(name) {
        this.name = name
        this._state = {}
        this._subscriptions = {}
        this._nextId = 0
        this._cached_key = undefined
    }

    create(entity, data) {
        data.id = entity
        this._state[entity] = data
        this.publish('create', entity)
        for (const key in data) {
            this.publish('set', { entity, key, value: data[key] })
        }
    }

    set(entity, key, value) {
        this._state[entity][key] = value;
        this.publish('set', { entity, key, value })
    }

    remove(entity, key) {
        delete this._state[entity][key];
        this.publish('remove', { entity, key })
    }

    delete(entity) {
        delete this._state[entity];
        this.publish('delete', entity)
    }

    get(entity) {
        return this._state[entity]
    }

    apply(data) {
        const parts = data.trim().split(" ")
        const op = parseInt(parts[0])
        const ent = parts[1] + "-" + parts[2]
        const last = parts[parts.length - 1]
        let key
        if (op === 3 || op === 4 || op === 5) {
            if (parts.length == 4) {
                key = this._cached_key
            } else {
                key = parts[3]
                this._cached_key = key
            }
        }
        if (op === 1) {
            this.create(ent, JSON.parse(last) || {})
        } else if (op === 2) {
            this.delete(ent)
        } else if (op === 3) {
            this.set(ent, key, JSON.parse(last))
        } else if (op === 4) {
            this.set(ent, key, JSON.parse(last))
        } else if (op === 5) {
            this.remove(ent, key)
        }
    }

    publish(eventType, arg) {
        if(!this._subscriptions[eventType])
            return
        Object.keys(this._subscriptions[eventType])
              .forEach(id => this._subscriptions[eventType][id](arg))
    }

    subscribe(eventType, callback) {
        const id = this._nextId++
        if(!this._subscriptions[eventType])
            this._subscriptions[eventType] = {}
        this._subscriptions[eventType][id] = callback
        return {
            unsubscribe: () => {
                delete this._subscriptions[eventType][id]
                if(Object.keys(this._subscriptions[eventType]).length === 0)
                    delete this._subscriptions[eventType]
            }
        }
    }
}

function getWorld(name) {
    if (states[name] === undefined) {
        const world = new WorldState(name)
        objects[name] = {}
        states[name] = world
        world.subscribe('set', data => {
            var entity = world.get(data.entity)
            if (entity.spr) {
                updateSprite(world.name, entity)
            }
        })
    }
    return states[name]
}

function listen(channels) {
    let evtSource = new EventSource("/events?" + channels.map(c => `channel=${c}`).join('&'), { 
        withCredentials: true,
    })
    evtSource.onmessage = event => {
        const data = JSON.parse(event.data)
        const world = getWorld(data.chn)
        for (let msg of data.evt) {
            world.apply(msg)
        }
    }
}

function createMaterial(name) {
    const map = new THREE.TextureLoader().load(`static/assets/${name}.png`)
    materials[name] = new THREE.SpriteMaterial({ map: map })
}

var states = {}
var objects = {}
var materials = {}

var width = window.innerWidth, height = window.innerHeight

const scene = new THREE.Scene()
const camera = new THREE.PerspectiveCamera(75, width / height, 0.01, 10)
const renderer = new THREE.WebGLRenderer()
renderer.setSize(width, height)
renderer.setAnimationLoop(animate)
document.body.appendChild(renderer.domElement)

createMaterial('boid')

camera.position.z = 5

function animate() {
	//cube.rotation.x += 0.01;
	//cube.rotation.y += 0.01;
	renderer.render(scene, camera);
}

function updateSprite(world, entity) {
    var id = entity.id
    var index = entity.sid || 0
    var pos = entity.pos || [0, 0]
    pos[0] = pos[0] / width * 8 - 1
    pos[1] = pos[1] / height * 8 - 1
    if (!objects[world][id]) {
        const sprite = new THREE.Sprite(materials[entity.spr])
        sprite.scale.set(.01, .01, .01)
        sprite.position.set(pos[0], pos[1], 0)
        scene.add(sprite)
        objects[world][id] = sprite
    }
    else {
        var sprite = objects[world][id]
        sprite.position.set(pos[0], pos[1], 0)
        sprite.material = materials[entity.spr]
    }
}

listen(['world'])
