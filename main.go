package main

import (
	"flag"
	"fmt"
	"log"
	"math"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Game state structures
type State struct {
	Player   Player
	Exit     *Loc
	MyKey    *Loc
	Floor    map[int]map[int]bool // x:y:floor
	Walls    map[int]map[int]bool // x:y:wall
	SawEnemy time.Time
	Enemy    *Loc
	Ammo     []Item
	Food     []Item
}

type Player struct {
	Name   string
	Loc    Loc
	Health int
	Ammo   int
	HasKey bool
}

type Loc struct {
	X int
	Y int
}

type Item struct {
	Loc  Loc
	Seen time.Time
}

// Global variables
var colorMap = map[string]string{"warrior": "red", "valkyrie": "blue", "elf": "green", "wizard": "yellow"}
var GameState = State{
	Floor: make(map[int]map[int]bool),
	Walls: make(map[int]map[int]bool),
	Ammo:  make([]Item, 0),
	Food:  make([]Item, 0),
}
var wallMutex sync.Mutex
var ammoMutex sync.Mutex
var foodMutex sync.Mutex
var shotDelay = 2

func main() {
	host := flag.String("host", "127.0.0.1", "Host")
	port := flag.Int("port", 11000, "Port")
	name := flag.String("name", "dvdbot", "Name")
	flag.Parse()

	connString := fmt.Sprintf("%s:%d", *host, *port)
	s, err := net.ResolveUDPAddr("udp4", connString)
	if err != nil {
		log.Fatal(err)
	}
	conn, err := net.DialUDP("udp", nil, s)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()
	log.Printf("Connected to %s\n", conn.RemoteAddr())
	join(*name, conn)

	go readLoop(conn) // background thread to capture and parse game state messages from server
	writeLoop(conn)
}

// Receive game updates from the sever and update our GameState structure
func readLoop(conn *net.UDPConn) {
	for {
		var msg = make([]byte, 1024)
		n, _, err := conn.ReadFromUDP(msg)
		if err != nil {
			log.Println("ERROR: " + err.Error())
		}
		if n > 0 {
			msgString := string(msg)
			msgParts := strings.Split(msgString, ":")
			msgType := msgParts[0]
			strippedParamString := strings.TrimRight(msgParts[1], "\x00")
			msgParams := strings.Split(strippedParamString, ",")
			switch msgType {
			case "playerjoined":
				x, _ := strconv.Atoi(msgParams[2])
				y, _ := strconv.Atoi(msgParams[3])
				GameState.Player = Player{Name: msgParams[0], Loc: Loc{X: x, Y: y}}
			case "playerupdate":
				xf, _ := strconv.ParseFloat(msgParams[0], 32)
				x := int(xf)
				GameState.Player.Loc.X = x
				yf, _ := strconv.ParseFloat(msgParams[1], 32)
				y := int(yf)
				GameState.Player.Loc.Y = y
				health, _ := strconv.Atoi(msgParams[2])
				GameState.Player.Health = health
				ammo, _ := strconv.Atoi(msgParams[3])
				GameState.Player.Ammo = ammo
				if strings.HasPrefix(msgParams[4], "True") {
					GameState.Player.HasKey = true
				} else {
					GameState.Player.HasKey = false
				}
			case "exit":
				if GameState.Exit == nil {
					x, _ := strconv.Atoi(msgParams[0])
					y, _ := strconv.Atoi(msgParams[1])
					GameState.Exit = &Loc{X: x, Y: y}
				}
			case "nearbyitem":
				item := msgParams[0]
				x, _ := strconv.Atoi(msgParams[1])
				y, _ := strconv.Atoi(msgParams[2])
				myKeyName := colorMap[GameState.Player.Name] + "key"
				if item == myKeyName {
					if GameState.MyKey == nil {
						GameState.MyKey = &Loc{X: x, Y: y}
					}
				} else if item == "ammo" {
					addAmmo(x, y)
				} else if item == "food" {
					addFood(x, y)
				}
			case "nearbyplayer":
				GameState.SawEnemy = time.Now()
				xf, _ := strconv.ParseFloat(msgParams[2], 32)
				x := int(xf)
				yf, _ := strconv.ParseFloat(msgParams[3], 32)
				y := int(yf)
				GameState.Enemy = &Loc{X: x, Y: y}
			case "nearbywalls":
				for i := 0; i < len(msgParams)-1; i += 2 {
					x, _ := strconv.Atoi(msgParams[i])
					y, _ := strconv.Atoi(msgParams[i+1])
					setWall(x, y)
				}
			case "nearbyfloors":
			default:
				log.Println(msgString)
			}
		}
	}
}

// Threadsafe setters to allow the readloop to set these values while forcing the writeloop to wait to read them
func setWall(x int, y int) {
	wallMutex.Lock()
	defer wallMutex.Unlock()
	_, ok := GameState.Walls[x]
	if !ok {
		GameState.Walls[x] = make(map[int]bool)
	}
	GameState.Walls[x][y] = true
}

func addFood(x int, y int) {
	foodMutex.Lock()
	defer foodMutex.Unlock()
	food := GameState.Food
	food = append(food, Item{Loc: Loc{X: x, Y: y}, Seen: time.Now()})
	GameState.Food = food
}

func addAmmo(x int, y int) {
	ammoMutex.Lock()
	defer ammoMutex.Unlock()
	ammo := GameState.Ammo
	ammo = append(ammo, Item{Loc: Loc{X: x, Y: y}, Seen: time.Now()})
	GameState.Ammo = ammo
}

// The main game logic, responsible for writing move messages to the server
func writeLoop(conn *net.UDPConn) {
	dir := "ne"
	targetItem := "key"
	shotCount := shotDelay
	for {
		if GameState.Player.Ammo == 0 {
			targetItem = "ammo"
		} else if GameState.Player.Health < 2 {
			targetItem = "food"
			/* 			} else if GameState.Player.HasKey {
			   				targetItem = "exit"
			   			} else {
			   				targetItem = "key"
			   			} */
		} else {
			targetItem = "enemy"
		}
		log.Printf("Target: %s\n", targetItem)
		lastLoc := GameState.Player.Loc
		switch targetItem {
		case "key":
			if GameState.MyKey != nil && canSeeItem(GameState.Player.Loc, *GameState.MyKey) {
				moveTo(*GameState.MyKey, conn)
			} else {
				moveToDir(dir, conn)
			}
		case "exit":
			if GameState.Exit != nil && canSeeItem(GameState.Player.Loc, *GameState.Exit) {
				moveTo(*GameState.Exit, conn)
			} else {
				moveToDir(dir, conn)
			}
		case "ammo":
			// This is quite dumb, we should find the nearest ammo we can see and move to it
			if len(GameState.Ammo) > 0 && canSeeItem(GameState.Player.Loc, GameState.Ammo[0].Loc) {
				moveTo(GameState.Ammo[0].Loc, conn)
			} else {
				moveToDir(dir, conn)
			}
		case "food":
			// This is quite dumb, we should find the nearest food we can see and move to it
			if len(GameState.Food) > 0 && canSeeItem(GameState.Player.Loc, GameState.Food[0].Loc) {
				log.Printf("Heading for food at (%d,%d)\n", GameState.Food[0].Loc.X, GameState.Food[0].Loc.Y)
				moveTo(GameState.Food[0].Loc, conn)
			} else {
				moveToDir(dir, conn)
			}
		case "enemy":
			// we need to expire enemy locations.  We can end up waiting on a position where a player died,
			// or went out of range.
			if GameState.Enemy != nil && canSeeItem(GameState.Player.Loc, *GameState.Enemy) {
				log.Printf("Heading for enemy at (%d,%d)\n", GameState.Enemy.X, GameState.Enemy.Y)
				moveTo(*GameState.Enemy, conn)
			} else {
				moveToDir(dir, conn)
			}
		}
		time.Sleep(100 * time.Millisecond) // don't DDoS the server
		dir = newDirection(dir, lastLoc, GameState.Player.Loc)
		if shotCount == 0 {
			shoot(conn)
			shotCount = shotDelay
		} else {
			shotCount--
		}
		expireItems()
	}
}

// check whether we have line of sight to an item (i.e. a wall is not in the way)
// brute force: check every wall.  could improve with BSP if needed
func canSeeItem(playerLoc Loc, itemLoc Loc) bool {
	wallMutex.Lock()
	defer wallMutex.Unlock()
	for x := range GameState.Walls {
		for y, wall := range GameState.Walls[x] {
			if wall {
				if intersects(playerLoc, itemLoc, x, y) {
					return false
				}
			}
		}
	}
	return true
}

// does a given wall tile intersect the line between us and the item?
func intersects(playerLoc Loc, itemLoc Loc, wallX int, wallY int) bool {
	wallCorner1 := Loc{X: wallX - 4, Y: wallY - 4}
	wallCorner2 := Loc{X: wallX + 4, Y: wallY + 4}

	a1 := itemLoc.Y - playerLoc.Y
	b1 := playerLoc.X - itemLoc.X
	c1 := a1*(playerLoc.X) + b1*playerLoc.Y

	// a1 is always 0, horizontal line
	b2 := (wallX + 4) - (wallX - 4)
	c2 := b2 * (wallY - 4)

	determinant := a1 * b2

	if determinant != 0 {
		x := float64(b2*c1-b1*c2) / float64(determinant)
		y := float64(a1*c2) / float64(determinant)
		crossingPoint := Loc{X: int(x), Y: int(y)}
		// crossing point must be within wall & within player/item bounding box
		if isWithinBounds(crossingPoint, playerLoc, itemLoc) && isWithinBounds(crossingPoint, wallCorner1, wallCorner2) {
			return true
		}
	}

	a2 := (wallY - 4) - (wallY + 4)
	// b2 is always 0, vertical line
	c2 = a2 * (wallX - 4)

	determinant = 0 - a2*b1

	if determinant != 0 {
		x := float64(0-b1*c2) / float64(determinant)
		y := float64(a1*c2-a2*c1) / float64(determinant)
		crossingPoint := Loc{X: int(x), Y: int(y)}
		// crossing point must be within wall & within player/item bounding box
		if isWithinBounds(crossingPoint, playerLoc, itemLoc) && isWithinBounds(crossingPoint, wallCorner1, wallCorner2) {
			return true
		}
	}

	return false
}

// is the given point within the bounds created between p1 and p2?
func isWithinBounds(point Loc, p1 Loc, p2 Loc) bool {
	minX := math.Min(float64(p1.X), float64(p2.X))
	maxX := math.Max(float64(p1.X), float64(p2.X))
	minY := math.Min(float64(p1.Y), float64(p2.Y))
	maxY := math.Max(float64(p1.Y), float64(p2.Y))
	return point.X >= int(minX) && point.X <= int(maxX) && point.Y >= int(minY) && point.Y <= int(maxY)
}

// pick a new direction to go in - if we hit a wall, bounce at a 90 degree angle
// needs to check if the wall we encountered (our position didn't change in one or both axes) was horizontal or vertical
func newDirection(oldDir string, lastLoc Loc, currentLoc Loc) string {
	sensitivity := 1 // there may be some jitter so assume we are stationary if our position varied by less than this amount
	xUnchanged := lastLoc.X < (currentLoc.X+sensitivity) && lastLoc.X > (currentLoc.X-sensitivity)
	yUnchanged := lastLoc.Y < (currentLoc.Y+sensitivity) && lastLoc.Y > (currentLoc.Y-sensitivity)
	newDir := oldDir
	if xUnchanged {
		switch oldDir {
		case "ne":
			newDir = "nw"
		case "se":
			newDir = "sw"
		case "sw":
			newDir = "se"
		case "nw":
			newDir = "ne"
		}
	} else if yUnchanged {
		switch oldDir {
		case "ne":
			newDir = "se"
		case "se":
			newDir = "ne"
		case "sw":
			newDir = "nw"
		case "nw":
			newDir = "sw"
		}
	}
	return newDir
}

// food and ammo may have been picked up but the game doesn't tell us
// delete any items that we haven't seen within the last 5 seconds
func expireItems() {
	deadline := time.Now().Add(-5 * time.Second)
	ammoMutex.Lock()
	newAmmo := make([]Item, 0)
	for _, a := range GameState.Ammo {
		if a.Seen.After(deadline) {
			// less than 5s since we saw this, keep it
			newAmmo = append(newAmmo, a)
		}
	}
	GameState.Ammo = newAmmo
	ammoMutex.Unlock()

	foodMutex.Lock()
	newFood := make([]Item, 0)
	for _, f := range GameState.Food {
		if f.Seen.After(deadline) {
			// less than 5s since we saw this, keep it
			newFood = append(newFood, f)
		}
	}
	GameState.Food = newFood
	foodMutex.Unlock()
}

// if there's an enemy in sight, shoot in its general direction
func shoot(conn *net.UDPConn) {
	var dir string
	if GameState.SawEnemy.After(time.Now().Add(-1*time.Second)) && canSeeItem(GameState.Player.Loc, *GameState.Enemy) {
		if GameState.Enemy.X == GameState.Player.Loc.X {
			if GameState.Enemy.Y > GameState.Player.Loc.Y {
				dir = "s"
			} else {
				dir = "n"
			}
		} else if GameState.Enemy.Y == GameState.Player.Loc.Y {
			if GameState.Enemy.X > GameState.Player.Loc.X {
				dir = "e"
			} else {
				dir = "w"
			}
		} else if GameState.Enemy.X > GameState.Player.Loc.X {
			if GameState.Enemy.Y > GameState.Player.Loc.Y {
				dir = "se"
			} else {
				dir = "ne"
			}
		} else {
			if GameState.Enemy.Y > GameState.Player.Loc.Y {
				dir = "sw"
			} else {
				dir = "nw"
			}
		}
		face(dir, conn)
		fire(conn)
	}
}

// format the messages as needed and send to the server
func join(name string, conn *net.UDPConn) {
	joinString := "requestjoin:" + name
	conn.Write([]byte(joinString))
}

func face(dir string, conn *net.UDPConn) {
	msgString := "facedirection:" + dir
	conn.Write([]byte(msgString))
}

func moveTo(to Loc, conn *net.UDPConn) {
	msgString := fmt.Sprintf("moveto:%d,%d", to.X, to.Y)
	conn.Write([]byte(msgString))
}

// move in a direction, but use the server's moveto command
func moveToDir(dir string, conn *net.UDPConn) {
	x := GameState.Player.Loc.X
	y := GameState.Player.Loc.Y
	switch dir {
	case "ne":
		y -= 10
		x += 10
	case "se":
		y += 10
		x += 10
	case "sw":
		y += 10
		x -= 10
	case "nw":
		y -= 10
		x -= 10
	}
	msgString := fmt.Sprintf("moveto:%d,%d", x, y)
	conn.Write([]byte(msgString))
}

func moveDir(dir string, conn *net.UDPConn) {
	msgString := fmt.Sprintf("movedirection:%s", dir)
	conn.Write([]byte(msgString))
}

func fire(conn *net.UDPConn) {
	msgString := "fire:"
	conn.Write([]byte(msgString))
}
