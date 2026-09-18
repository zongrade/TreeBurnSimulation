package render

import (
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"time"

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

	// Velocity для плавного движения
	velocityX   float64
	velocityY   float64
	maxPanSpeed float64

	// Для перетаскивания средней кнопкой
	isDragging bool
	dragLastX  int
	dragLastY  int

	accumulator float64
	tickCount   int

	// Для автосохранения
	screenshotCounter   int
	lastRegularSaveTick int // последний регулярный скриншот
	lastFireSaveTick    int // последний адаптивный скриншот при пожаре

	// Логирование и проверка зависимостей
	logger      *Logger
	ffmpegReady bool

	// Уникальный ID сессии
	sessionID string

	// UI
	paused   bool
	finished bool
	buttons  []Button

	isLargeFire bool // текущий статус крупного пожара

	debug bool
}

func NewGame(world *sim.World) *Game {
	width := world.Width
	height := world.Height

	// Генерируем уникальный ID сессии
	cfg := world.Config()
	sessionID := fmt.Sprintf("%s_%s_seed%d",
		cfg.Save.Prefix,
		time.Now().Format("2006-01-02_15-04-05"),
		cfg.Seed,
	)

	g := &Game{
		world:       world,
		image:       ebiten.NewImage(width, height),
		pixels:      make([]byte, width*height*4),
		cameraX:     float64(width) / 4,
		cameraY:     float64(height) / 2,
		zoom:        0.75,
		minZoom:     0.1,
		maxZoom:     10.0,
		maxPanSpeed: 200,
		sessionID:   sessionID,
		logger:      NewLogger(10, cfg.Save.LogToFile, filepath.Join(cfg.Save.Dir, sessionID)),
		debug:       true,
	}

	// Первый рендер
	g.checkDependencies()
	g.initButtons()
	g.updatePixels()

	return g
}

func (g *Game) Update() error {
	if g.finished {
		return ebiten.Termination
	}

	dt := 1.0 / 60.0

	// Обработка кликов по кнопкам
	if inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft) {
		mx, my := ebiten.CursorPosition()
		for i := range g.buttons {
			if g.buttons[i].Contains(mx, my) {
				g.buttons[i].Action()
			}
		}
	}

	// Обработка ввода
	g.handleInput(dt)

	// Применяем velocity к позиции камеры
	g.cameraX += g.velocityX * dt
	g.cameraY += g.velocityY * dt

	// Плавное затухание velocity (трение)
	// Коэффициент затухания: чем меньше, тем сильнее торможение
	friction := 0.85
	g.velocityX *= friction
	g.velocityY *= friction

	// Если скорость очень маленькая, обнуляем её
	if absF(g.velocityX) < 0.5 {
		g.velocityX = 0
	}
	if absF(g.velocityY) < 0.5 {
		g.velocityY = 0
	}

	// Симуляция только если не на паузе
	if !g.paused {
		// Симуляция с фиксированным timestep
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

			// Проверяем, нужно ли сохранять
			saveCfg := g.world.SaveConfig()
			if saveCfg.Enabled && saveCfg.EveryTicks > 0 {
				// Проверяем статус крупного пожара
				if saveCfg.AdaptiveEnabled {
					g.checkLargeFire(saveCfg)
				}

				savedThisTick := false

				// Регулярный скриншот (всегда)
				if g.tickCount-g.lastRegularSaveTick >= saveCfg.EveryTicks {
					g.updatePixels()
					g.saveScreenshot()
					g.lastRegularSaveTick = g.tickCount
					g.lastFireSaveTick = g.tickCount // чтобы не делать дубль сразу
					savedThisTick = true
				}

				// Адаптивный скриншот при крупном пожаре
				if saveCfg.AdaptiveEnabled && g.isLargeFire && saveCfg.EveryTicksOnFire > 0 {
					// Проверяем, не сделали ли мы уже регулярный скриншот на этом тике
					if !savedThisTick && g.tickCount-g.lastFireSaveTick >= saveCfg.EveryTicksOnFire {
						g.updatePixels()
						g.saveScreenshot()
						g.lastFireSaveTick = g.tickCount
					}
				}
			}
		}

		// Обновляем текстуру только если мир изменился (или мы двигаемся)
		if needsRender || g.velocityX != 0 || g.velocityY != 0 || g.isDragging {
			g.updatePixels()
		}
	}
	return nil
}

func (g *Game) handleInput(dt float64) {
	mx, _ := ebiten.CursorPosition()
	overSim := mx > panelWidth // курсор над областью симуляции

	// === Перетаскивание средней кнопкой мыши (только над симуляцией) ===
	if overSim {
		if inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonMiddle) {
			g.isDragging = true
			g.dragLastX, g.dragLastY = ebiten.CursorPosition()
		}

		if inpututil.IsMouseButtonJustReleased(ebiten.MouseButtonMiddle) {
			g.isDragging = false
		}

		if g.isDragging {
			cx, cy := ebiten.CursorPosition()
			deltaX := cx - g.dragLastX
			deltaY := cy - g.dragLastY

			// Движение мыши в пикселях конвертируем в мировые координаты
			g.cameraX -= float64(deltaX) / g.zoom
			g.cameraY -= float64(deltaY) / g.zoom

			g.dragLastX = cx
			g.dragLastY = cy

			// Во время перетаскивания обнуляем velocity (иначе будет инерция)
			g.velocityX = 0
			g.velocityY = 0
		}
	} else {
		// Если мышь ушла с симуляции, прекращаем drag
		g.isDragging = false
	}
	// === Ускорение ===
	accelMultiplier := 1.0
	if ebiten.IsKeyPressed(ebiten.KeyShift) {
		accelMultiplier = 5.0
	}

	// Ускорение камеры (сила, которая меняет velocity)
	// Делим на zoom, чтобы скорость в экранных пикселях была одинаковой при любом зуме
	accel := 3000.0 * accelMultiplier / g.zoom

	// === WASD с плавным ускорением ===
	if ebiten.IsKeyPressed(ebiten.KeyW) || ebiten.IsKeyPressed(ebiten.KeyUp) {
		g.velocityY -= accel * dt
	}
	if ebiten.IsKeyPressed(ebiten.KeyS) || ebiten.IsKeyPressed(ebiten.KeyDown) {
		g.velocityY += accel * dt
	}
	if ebiten.IsKeyPressed(ebiten.KeyA) || ebiten.IsKeyPressed(ebiten.KeyLeft) {
		g.velocityX -= accel * dt
	}
	if ebiten.IsKeyPressed(ebiten.KeyD) || ebiten.IsKeyPressed(ebiten.KeyRight) {
		g.velocityX += accel * dt
	}

	// Ограничение максимальной скорости (в мировых координатах)
	maxSpeed := g.maxPanSpeed / g.zoom

	if g.velocityX > maxSpeed {
		g.velocityX = maxSpeed
	}
	if g.velocityX < -maxSpeed {
		g.velocityX = -maxSpeed
	}
	if g.velocityY > maxSpeed {
		g.velocityY = maxSpeed
	}
	if g.velocityY < -maxSpeed {
		g.velocityY = -maxSpeed
	}

	// === Зум колесом мыши (к курсору) ===
	if overSim {
		_, dy := ebiten.Wheel()
		if dy != 0 {
			zoomFactor := 1.1
			if dy < 0 {
				zoomFactor = 1.0 / zoomFactor
			}

			newZoom := g.zoom * zoomFactor
			if newZoom >= g.minZoom && newZoom <= g.maxZoom {
				cx, cy := ebiten.CursorPosition()
				screenW, screenH := ebiten.WindowSize()

				// Точка мира под курсором до зума
				worldX := (float64(cx)-float64(screenW)/2)/g.zoom + g.cameraX
				worldY := (float64(cy)-float64(screenH)/2)/g.zoom + g.cameraY

				g.zoom = newZoom

				// Корректируем камеру, чтобы точка под курсором осталась на месте
				g.cameraX = worldX - (float64(cx)-float64(screenW)/2)/g.zoom
				g.cameraY = worldY - (float64(cy)-float64(screenH)/2)/g.zoom
			}
		}
	}

	// === Переключение отладки ===
	if inpututil.IsKeyJustPressed(ebiten.KeyF1) {
		g.debug = !g.debug
	}

	// === Сброс камеры ===
	if inpututil.IsKeyJustPressed(ebiten.KeyR) {
		g.cameraX = 0
		g.cameraY = 0
		g.velocityX = 0
		g.velocityY = 0
		g.zoom = 0.75
	}

	// Ручное сохранение скриншота
	if inpututil.IsKeyJustPressed(ebiten.KeyF5) {
		g.updatePixels()
		g.saveScreenshot()
	}

	// === Пробел для паузы ===
	if inpututil.IsKeyJustPressed(ebiten.KeySpace) {
		g.paused = !g.paused
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

	// === Рисуем симуляцию (справа от панели) ===
	simAreaW := screenW - panelWidth
	simAreaH := screenH

	op.GeoM.Translate(-g.cameraX, -g.cameraY)
	op.GeoM.Scale(g.zoom, g.zoom)
	op.GeoM.Translate(float64(simAreaW)/2, float64(simAreaH)/2)

	op.Filter = ebiten.FilterNearest

	screen.DrawImage(g.image, op)

	// === Рисуем панель ===
	g.drawPanel(screen)
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

func (g *Game) saveScreenshot() {
	saveCfg := g.world.SaveConfig()

	// Формируем путь: базовая директория + sessionID
	sessionDir := filepath.Join(saveCfg.Dir, g.sessionID)

	// Создаём директорию, если её нет
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		g.logger.Error("Failed to create save directory: %v", err)
		return
	}

	// Увеличиваем счётчик
	g.screenshotCounter++

	digits := saveCfg.FilenameDigits

	// Определяем расширение файла
	ext := ".png"
	if saveCfg.Format == sim.FormatJPEG {
		ext = ".jpg"
	}

	format := fmt.Sprintf("%%0%dd%s", digits, ext)

	// Формируем имя файла
	filename := fmt.Sprintf(format, g.screenshotCounter)
	filePath := filepath.Join(sessionDir, filename)

	// Создаём файл
	file, err := os.Create(filePath)
	if err != nil {
		g.logger.Error("Failed to create file: %v", err)
		return
	}
	defer file.Close()

	// Создаём image.Image из пикселей
	width := g.world.Width
	height := g.world.Height

	img := image.NewRGBA(image.Rect(0, 0, width, height))

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			srcIdx := (y*width + x) * 4
			dstIdx := y*img.Stride + x*4

			img.Pix[dstIdx+0] = g.pixels[srcIdx+0] // R
			img.Pix[dstIdx+1] = g.pixels[srcIdx+1] // G
			img.Pix[dstIdx+2] = g.pixels[srcIdx+2] // B
			img.Pix[dstIdx+3] = g.pixels[srcIdx+3] // A
		}
	}

	// Сохраняем в нужном формате
	switch saveCfg.Format {
	case sim.FormatJPEG:
		quality := saveCfg.Quality
		if quality <= 0 {
			quality = 85
		}
		if quality > 100 {
			quality = 100
		}

		if err := jpeg.Encode(file, img, &jpeg.Options{Quality: quality}); err != nil {
			g.logger.Error("Failed to encode JPEG: %v", err)
			return
		}

	case sim.FormatPNG:
		// PNG с максимальным сжатием
		encoder := &png.Encoder{CompressionLevel: png.BestCompression}
		if err := encoder.Encode(file, img); err != nil {
			g.logger.Error("Failed to encode PNG: %v", err)
			return
		}

	default:
		g.logger.Error("Unknown save format: %s", saveCfg.Format)
		return
	}

	fmt.Printf("Saved: %s\n", filePath)
}

func (g *Game) initButtons() {
	btnW := panelWidth - 20
	btnH := 30
	btnX := 10
	startY := 10

	g.buttons = []Button{
		{
			X: btnX, Y: startY, W: btnW, H: btnH,
			Label: "Pause / Resume",
			Action: func() {
				g.paused = !g.paused
			},
		},
		{
			X: btnX, Y: startY + btnH + 10, W: btnW, H: btnH,
			Label: "Save Screenshot",
			Action: func() {
				g.updatePixels()
				g.saveScreenshot()
			},
		},
		{
			X: btnX, Y: startY + (btnH+10)*2, W: btnW, H: btnH,
			Label: "Finish & Make Video",
			Action: func() {
				g.finishAndMakeVideo()
			},
		},
	}
}

func (g *Game) drawPanel(screen *ebiten.Image) {
	// Фон панели
	panelRect := ebiten.NewImage(panelWidth, 1000)
	panelRect.Fill(color.RGBA{30, 30, 35, 255})

	op := &ebiten.DrawImageOptions{}
	screen.DrawImage(panelRect, op)

	// Кнопки
	for i := range g.buttons {
		b := &g.buttons[i]

		btnColor := color.RGBA{60, 60, 70, 255}
		if b.Contains(ebiten.CursorPosition()) {
			btnColor = color.RGBA{80, 80, 95, 255}
		}

		btnImg := ebiten.NewImage(b.W, b.H)
		btnImg.Fill(btnColor)

		op := &ebiten.DrawImageOptions{}
		op.GeoM.Translate(float64(b.X), float64(b.Y))
		screen.DrawImage(btnImg, op)

		// Текст кнопки
		ebitenutil.DebugPrintAt(screen, b.Label, b.X+5, b.Y+8)
	}

	statsY := 0
	if g.debug {
		// Статистика
		alive, burning := g.countCells()

		statsY = 130

		status := "Running"
		if g.paused {
			status = "Paused"
		}

		cfg := g.world.Config()

		statsText := fmt.Sprintf(
			"Status: %s\nFPS: %0.2f\nTick: %d\nTickRate: %d\nZoom: %0.2f\nTrees: %d\nBurning: %d\nSession:\n%s\nCamera: %0.1f, %0.1f",
			status,
			ebiten.ActualFPS(),
			g.tickCount,
			cfg.TickRate,
			g.zoom,
			alive,
			burning,
			g.sessionID,
			g.cameraX,
			g.cameraY,
		)

		ebitenutil.DebugPrintAt(screen, statsText, 10, statsY)
	}

	// Подсказки
	hintsY := statsY + 200
	hintsText := "Controls:\nWASD/Arrows - move\nShift - faster\nMouse wheel - zoom\nMiddle button - drag\nF1 - toggle debug\nF5 - screenshot"

	ebitenutil.DebugPrintAt(screen, hintsText, 10, hintsY)

	// Логи
	logsY := hintsY + 140
	logsText := "Logs:\n"

	entries := g.logger.GetEntries()

	// Показываем последние 5 записей
	startIdx := 0
	if len(entries) > 5 {
		startIdx = len(entries) - 5
	}

	for i := startIdx; i < len(entries); i++ {
		entry := entries[i]

		// Цвет зависит от уровня
		var prefix string
		switch entry.Level {
		case LogInfo:
			prefix = "[I]"
		case LogWarning:
			prefix = "[W]"
		case LogError:
			prefix = "[E]"
		}

		// Обрезаем длинные сообщения
		msg := entry.Message
		if len(msg) > 30 {
			msg = msg[:27] + "..."
		}

		logsText += fmt.Sprintf("%s %s\n", prefix, msg)
	}

	ebitenutil.DebugPrintAt(screen, logsText, 10, logsY)
}

func (g *Game) finishAndMakeVideo() {
	saveCfg := g.world.SaveConfig()
	sessionDir := filepath.Join(saveCfg.Dir, g.sessionID)

	// Финальное сохранение
	g.updatePixels()
	g.saveScreenshot()

	// Проверяем, установлен ли ffmpeg
	if !g.ffmpegReady {
		g.logger.Error("Cannot create video: ffmpeg not installed")
		g.logger.Info("Screenshots saved in: %s", sessionDir)
		g.finished = true
		return
	}

	digits := saveCfg.FilenameDigits

	// Определяем расширение файла
	ext := ".png"
	if saveCfg.Format == sim.FormatJPEG {
		ext = ".jpg"
	}

	inputPattern := fmt.Sprintf("%%0%dd%s", digits, ext)
	inputPath := filepath.Join(sessionDir, inputPattern)
	outputPath := filepath.Join(sessionDir, "output.mp4")

	g.logger.Info("Creating video from %d screenshots...", g.screenshotCounter)

	// ffmpeg -framerate 10 -i %06d.png -c:v libx264 -pix_fmt yuv420p output.mp4
	cmd := exec.Command("ffmpeg",
		"-framerate", fmt.Sprintf("%d", g.videoFramerate()),
		"-i", inputPath,
		"-c:v", "libx264",
		"-pix_fmt", "yuv420p",
		"-y",
		outputPath,
	)

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	fmt.Println("Creating video...")
	if err := cmd.Run(); err != nil {
		fmt.Printf("Failed to create video: %v\n", err)
	} else {
		fmt.Printf("Video saved: %s\n", outputPath)
	}

	g.finished = true
}

func (g *Game) videoFramerate() int {
	saveCfg := g.world.SaveConfig()
	if saveCfg.VideoFramerate > 0 {
		return saveCfg.VideoFramerate
	}
	return g.world.TickRate()
}

func (g *Game) checkDependencies() {
	// Проверяем ffmpeg
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		g.ffmpegReady = false
		g.logger.Warning("ffmpeg not found in PATH. Video creation will be disabled.")
		g.logger.Info("Install ffmpeg from https://ffmpeg.org/download.html")
	} else {
		g.ffmpegReady = true
		g.logger.Info("ffmpeg found. Video creation enabled.")
	}
}

func (g *Game) checkLargeFire(saveCfg sim.SaveConfig) {
	burningCount := g.world.BurningCount()
	alive, _ := g.countCells()

	wasLargeFire := g.isLargeFire
	isFire := false

	if saveCfg.FireThreshold > 0 {
		// Используем абсолютный порог
		isFire = burningCount >= saveCfg.FireThreshold
	} else if saveCfg.FirePercentThreshold > 0 && alive > 0 {
		// Используем процентный порог
		firePercent := float64(burningCount) / float64(alive)
		isFire = firePercent >= saveCfg.FirePercentThreshold
	}

	g.isLargeFire = isFire

	// Логируем переход в режим крупного пожара
	if isFire && !wasLargeFire {
		g.logger.Info("Large fire detected: %d burning cells. Increasing save frequency.", burningCount)
	}

	// Логируем выход из режима крупного пожара
	if !isFire && wasLargeFire {
		g.logger.Info("Fire under control: %d burning cells. Normal save frequency.", burningCount)
	}
}

// Вспомогательная функция для float64
func absF(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

const panelWidth = 250

type Button struct {
	X, Y, W, H int
	Label      string
	Action     func()
}

func (b *Button) Contains(x, y int) bool {
	return x >= b.X && x <= b.X+b.W && y >= b.Y && y <= b.Y+b.H
}
