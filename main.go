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

type State struct {
	Player   Player
	Exit     *Loc
	MyKey    *Loc
	Floor    map[int]map[int]bool // x:y:floor
	Walls    map[int]map[int]bool // x:y:wall
	SawEnemy time.Time
	Enemy    *Loc
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

var colorMap = map[string]string{"warrior": "red", "valkyrie": "blue", "elf": "green", "wizard": "yellow"}
var GameState = State{
	Floor: make(map[int]map[int]bool),
	Walls: make(map[int]map[int]bool),
}
var floorMutex sync.Mutex
var wallMutex sync.Mutex

func setFloor(x int, y int) {
	floorMutex.Lock()
	defer floorMutex.Unlock()
	_, ok := GameState.Floor[x]
	if !ok {
		GameState.Floor[x] = make(map[int]bool)
	}
	GameState.Floor[x][y] = true
}

func setWall(x int, y int) {
	wallMutex.Lock()
	defer wallMutex.Unlock()
	_, ok := GameState.Walls[x]
	if !ok {
		GameState.Walls[x] = make(map[int]bool)
	}
	GameState.Walls[x][y] = true
}

func getWall(x int, y int) bool {
	wallMutex.Lock()
	defer wallMutex.Unlock()
	_, ok := GameState.Walls[x]
	if !ok {
		return false
	}
	return GameState.Walls[x][y]
}

func intersects(playerLoc Loc, itemLoc Loc, wallX int, wallY int) bool {
	a1 := itemLoc.Y - playerLoc.Y
	b1 := playerLoc.X - itemLoc.X
	c1 := a1*(playerLoc.X) + b1*playerLoc.Y

	a2 := wallY - wallY
	b2 := (wallX - 4) - (wallX + 4)
	c2 := a2*(wallX-4) + b2*(wallY)

	determinant := a1*b2 - a2*b1

	if determinant != 0 {
		x := (b2*c1 - b1*c2) / determinant
		y := (a1*c2 - a2*c1) / determinant
		// is the intersection point between us and the item?
		if isBetweenPlayerAndItem(Loc{X: x, Y: y}, playerLoc, itemLoc) {
			return true
		}
	}

	a2 = (wallY - 4) - (wallY + 4)
	b2 = wallX - wallX
	c2 = a2*(wallX) + b2*(wallY-4)

	determinant = a1*b2 - a2*b1

	if determinant != 0 {
		x := (b2*c1 - b1*c2) / determinant
		y := (a1*c2 - a2*c1) / determinant
		if isBetweenPlayerAndItem(Loc{X: x, Y: y}, playerLoc, itemLoc) {
			return true
		}
	}

	return false
}

func isBetweenPlayerAndItem(point Loc, player Loc, item Loc) bool {
	minX := math.Min(float64(player.X), float64(item.X))
	maxX := math.Max(float64(player.X), float64(item.X))
	minY := math.Min(float64(player.Y), float64(item.Y))
	maxY := math.Max(float64(player.Y), float64(item.Y))
	return point.X >= int(minX) && point.X <= int(maxX) && point.Y >= int(minY) && point.Y <= int(maxY)
}

func canSeeItem(playerLoc Loc, itemLoc Loc) bool {
	wallMutex.Lock()
	defer wallMutex.Unlock()
	for x := range GameState.Walls {
		for y, wall := range GameState.Walls[x] {
			if wall {
				log.Printf("Checking wall at (%d,%d) betwen player at (%d,%d) and item at (%d,%d)...",
					x, y, playerLoc.X, playerLoc.Y, itemLoc.X, itemLoc.Y)
				if intersects(playerLoc, itemLoc, x, y) {
					fmt.Println(" Intersects")
					return true
				} else {
					fmt.Println(" Does not Intersect")
				}
			}
		}
	}
	return false
}

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

	go readLoop(conn)
	writeLoop(conn)

}

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
				}
			case "nearbyfloors":
				for i := 0; i < len(msgParams)-1; i += 2 {
					x, _ := strconv.Atoi(msgParams[i])
					y, _ := strconv.Atoi(msgParams[i+1])
					setFloor(x, y)
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
			default:
				log.Println(msgString)
			}
		}
	}
}

func newDirection(oldDir string, lastLoc Loc, currentLoc Loc) string {
	//log.Printf("Player at (%d,%d), was at (%d,%d)\n", currentLoc.X, currentLoc.Y, lastLoc.X, lastLoc.Y)
	sensitivity := 1
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

func writeLoop(conn *net.UDPConn) {
	dir := "ne"
	targetItem := "key"
	for {
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
		}
		if GameState.Player.HasKey {
			targetItem = "exit"
		}
		time.Sleep(200 * time.Millisecond)
		dir = newDirection(dir, lastLoc, GameState.Player.Loc)
		shoot(conn)

	}
}

func shoot(conn *net.UDPConn) {
	//directions := []string{"n", "ne", "e", "se", "s", "sw", "w", "nw"}
	var dir string
	if GameState.SawEnemy.After(time.Now().Add(-1 * time.Second)) {
		//log.Printf("Saw enemy at (%d,%d), player at (%d,%d)\n", GameState.Enemy.X, GameState.Enemy.Y,
		//	GameState.Player.Loc.X, GameState.Player.Loc.Y)
		//n := rand.Intn(7)
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

func moveToDir(dir string, conn *net.UDPConn) {
	x := GameState.Player.Loc.X
	y := GameState.Player.Loc.Y
	//log.Printf("Player at (%d,%d), ", x, y)
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
	//log.Printf("Moving to (%d,%d)\n", x, y)
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
