package render

import (
	"fmt"
	"image/color"

	"github.com/aizonwork/treeburnsimulation/sim"
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/ebitenutil"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
)

type Game struct {
	world  *sim.World
	image  *ebiten.Image
	pixels []byte

	cameraX float64
	cameraY float64
	zoom    float64
	minZoom float64
	maxZoom float64

	accumulator float64
	tickCount   int

	debug bool
}

func NewGame(world *sim.World) *Game {
	width := world.Width
	height := world.Height

	g := &Game{
		world:   world,
		image:   ebiten.NewImage(width, height),
		pixels:  make([]byte, width*height*4),
		cameraX: float64(width) / 2,
		cameraY: float64(height) / 2,
		zoom:    0.75,
		minZoom: 0.1,
		maxZoom: 10.0,
		debug:   true,
	}

	// Первый рендер
	g.updatePixels()

	return g
}

func (g *Game) Update() error {
	// Обработка ввода
	g.handleInput()

	// Симуляция с фиксированным timestep
	dt := 1.0 / 60.0
	g.accumulator += dt

	cfg := g.world.Config()
	simDt := 1.0 / float64(cfg.TickRate)

	// Ограничиваем accumulator
	if g.accumulator > 0.5 {
		g.accumulator = 0.5
	}

	needsRender := false

	for g.accumulator >= simDt {
		g.world.Tick()
		g.accumulator -= simDt
		g.tickCount++
		needsRender = true
	}

	// Обновляем текстуру только если мир изменился
	if needsRender {
		g.updatePixels()
	}

	return nil
}

func (g *Game) handleInput() {
	// Перемещение камеры
	panSpeed := 5.0 / g.zoom

	// Ускорение при зажатом Shift
	if ebiten.IsKeyPressed(ebiten.KeyShift) {
		panSpeed *= 5.0
	}

	if ebiten.IsKeyPressed(ebiten.KeyW) || ebiten.IsKeyPressed(ebiten.KeyUp) {
		g.cameraY -= panSpeed
	}
	if ebiten.IsKeyPressed(ebiten.KeyS) || ebiten.IsKeyPressed(ebiten.KeyDown) {
		g.cameraY += panSpeed
	}
	if ebiten.IsKeyPressed(ebiten.KeyA) || ebiten.IsKeyPressed(ebiten.KeyLeft) {
		g.cameraX -= panSpeed
	}
	if ebiten.IsKeyPressed(ebiten.KeyD) || ebiten.IsKeyPressed(ebiten.KeyRight) {
		g.cameraX += panSpeed
	}

	// Зум колесом мыши
	_, dy := ebiten.Wheel()
	if dy != 0 {
		zoomFactor := 1.1
		if dy < 0 {
			zoomFactor = 1.0 / zoomFactor
		}

		newZoom := g.zoom * zoomFactor
		if newZoom >= g.minZoom && newZoom <= g.maxZoom {
			// Зум к курсору мыши
			cx, cy := ebiten.CursorPosition()
			screenW, screenH := ebiten.WindowSize()

			worldX := (float64(cx)-float64(screenW)/2)/g.zoom + g.cameraX
			worldY := (float64(cy)-float64(screenH)/2)/g.zoom + g.cameraY

			g.zoom = newZoom

			g.cameraX = worldX - (float64(cx)-float64(screenW)/2)/g.zoom
			g.cameraY = worldY - (float64(cy)-float64(screenH)/2)/g.zoom
		}
	}

	// Переключение отладки
	if inpututil.IsKeyJustPressed(ebiten.KeyF1) {
		g.debug = !g.debug
	}

	// Сброс камеры
	if inpututil.IsKeyJustPressed(ebiten.KeyR) {
		g.cameraX = float64(g.world.Width) / 2
		g.cameraY = float64(g.world.Height) / 2
		g.zoom = 1.0
	}
}

func (g *Game) updatePixels() {
	width := g.world.Width
	height := g.world.Height
	stride := g.world.Stride

	cfg := g.world.Config()
	fireCfg := cfg.Fire

	maxBurn := float64(fireCfg.BurnBaseTicks) + fireCfg.BurnTicksPerHeight*255
	if maxBurn <= 0 {
		maxBurn = 1
	}

	i := 0

	for y := 0; y < height; y++ {
		baseIdx := (y + 1) * stride

		for x := 0; x < width; x++ {
			idx := baseIdx + (x + 1)

			h := g.world.Heights[idx]
			b := g.world.Burns[idx]

			var r, g_, b_ uint8

			if h == 0 && b == 0 {
				// Пустая клетка
				r, g_, b_ = 40, 35, 30
			} else if b > 0 {
				// Горящее дерево
				intensity := 180 + uint8(75*float64(b)/maxBurn)
				r = intensity
				g_ = intensity / 2
				b_ = 0
			} else {
				// Живое дерево
				brightness := 60 + uint8(float32(h)/255.0*140.0)
				r = 20
				g_ = brightness
				b_ = 20
			}

			g.pixels[i+0] = r
			g.pixels[i+1] = g_
			g.pixels[i+2] = b_
			g.pixels[i+3] = 255

			i += 4
		}
	}

	g.image.WritePixels(g.pixels)
}

func (g *Game) Draw(screen *ebiten.Image) {
	screen.Fill(color.RGBA{10, 10, 10, 255})

	op := &ebiten.DrawImageOptions{}

	screenSize := screen.Bounds().Size()
	screenW, screenH := screenSize.X, screenSize.Y

	op.GeoM.Translate(-g.cameraX, -g.cameraY)
	op.GeoM.Scale(g.zoom, g.zoom)
	op.GeoM.Translate(float64(screenW)/2, float64(screenH)/2)

	op.Filter = ebiten.FilterNearest

	screen.DrawImage(g.image, op)

	// Отладочная информация
	if g.debug {
		alive, burning := g.countCells()
		cfg := g.world.Config()

		debugText := fmt.Sprintf(
			"FPS: %0.2f\nTPS: %d\nTick: %d\nZoom: %0.2f\nTrees: %d\nBurning: %d\nCamera: %0.1f, %0.1f",
			ebiten.ActualFPS(),
			cfg.TickRate,
			g.tickCount,
			g.zoom,
			alive,
			burning,
			g.cameraX,
			g.cameraY,
		)

		ebitenutil.DebugPrint(screen, debugText)
	}
}

func (g *Game) Layout(outsideWidth, outsideHeight int) (int, int) {
	return outsideWidth, outsideHeight
}

func (g *Game) countCells() (alive, burning int) {
	width := g.world.Width
	height := g.world.Height
	stride := g.world.Stride

	for y := 1; y <= height; y++ {
		base := y * stride
		for x := 1; x <= width; x++ {
			i := base + x
			h := g.world.Heights[i]
			b := g.world.Burns[i]

			if h > 0 && b == 0 {
				alive++
			} else if h > 0 && b > 0 {
				burning++
			}
		}
	}

	return alive, burning
}
