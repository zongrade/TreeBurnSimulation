package sim

import (
	"math"
	"runtime"
	"sync"
)

type Chunk struct {
	ID int

	// В координатах padded-массива.
	// Рабочая область: x [X0, X1), y [Y0, Y1).
	X0, Y0 int
	X1, Y1 int
}

type World struct {
	cfg Config

	Width  int // ширина мира
	Height int // высота мира
	Stride int

	// Текущее состояние.
	Heights []uint8
	Burns   []uint8

	// Следующее состояние.
	NextHeights []uint8
	NextBurns   []uint8

	chunks []Chunk

	tick uint64
	rng  rng

	spawnThreshold uint64
	growThreshold  uint64

	fireThreshold []uint64

	burnDuration [256]uint8

	orthoWeight int
	diagWeight  int
}

func NewWorld(cfg Config) *World {
	if cfg.Width <= 0 {
		cfg.Width = 1000
	}
	if cfg.Height <= 0 {
		cfg.Height = 1000
	}
	if cfg.TickRate <= 0 {
		cfg.TickRate = 10
	}
	if cfg.WorkerCount <= 0 {
		cfg.WorkerCount = 12
	}
	if cfg.ChunkSize <= 0 {
		cfg.ChunkSize = 64
	}
	if cfg.Growth.MaxHeight == 0 {
		cfg.Growth.MaxHeight = 255
	}
	if cfg.Fire.DiagonalFactor < 0 {
		cfg.Fire.DiagonalFactor = 0
	}

	stride := cfg.Width + 2
	size := (cfg.Height + 2) * stride

	w := &World{
		cfg:    cfg,
		Width:  cfg.Width,
		Height: cfg.Height,
		Stride: stride,

		Heights: make([]uint8, size),
		Burns:   make([]uint8, size),

		NextHeights: make([]uint8, size),
		NextBurns:   make([]uint8, size),

		rng: rng(cfg.Seed),
	}

	w.orthoWeight = 100
	w.diagWeight = int(math.Round(cfg.Fire.DiagonalFactor * 100))

	w.precompute()
	w.buildDynamicChunks()

	return w
}

func (w *World) Tick() {
	dt := 1.0 / float64(w.cfg.TickRate)

	w.applyLightning(dt)
	w.parallelUpdate()

	// Теперь типы совпадают: []uint8 <-> []uint8
	w.Heights, w.NextHeights = w.NextHeights, w.Heights
	w.Burns, w.NextBurns = w.NextBurns, w.Burns

	w.tick++
}

func (w *World) precompute() {
	dt := 1.0 / float64(w.cfg.TickRate)

	w.spawnThreshold = rateThreshold(w.cfg.Growth.SpawnRatePerSecond, dt)
	w.growThreshold = rateThreshold(w.cfg.Growth.GrowRatePerSecond, dt)

	// Максимальный вес зависит от радиуса распространения
	radius := w.cfg.Fire.SpreadRadius
	if radius <= 0 {
		radius = 1
	}

	// Примерное количество соседей в радиусе (все клетки кроме центральной)
	maxNeighbors := (2*radius+1)*(2*radius+1) - 1
	maxWeight := maxNeighbors * w.orthoWeight // берём максимальный вес для запаса

	w.fireThreshold = make([]uint64, maxWeight+1)

	for weight := 0; weight <= maxWeight; weight++ {
		rate := w.cfg.Fire.SpreadRatePerSecond * float64(weight) / 100.0
		w.fireThreshold[weight] = rateThreshold(rate, dt)
	}

	for h := 0; h < 256; h++ {
		d := float64(w.cfg.Fire.BurnBaseTicks) +
			float64(h)*w.cfg.Fire.BurnTicksPerHeight

		if d < 1 {
			d = 1
		}
		if d > 255 {
			d = 255
		}

		w.burnDuration[h] = uint8(d)
	}
}

// Заполнение карты деревьями.
// density = 1.0 -> все клетки с деревьями.
func (w *World) FillTrees(density float64) {
	if density <= 0 {
		return
	}

	maxH := int(w.cfg.Growth.MaxHeight)
	if maxH <= 0 {
		maxH = 1
	}

	for y := 1; y <= w.Height; y++ {
		base := y * w.Stride
		for x := 1; x <= w.Width; x++ {
			i := base + x

			if density >= 1.0 || w.rng.float64() < density {
				h := uint8(1 + w.rng.intN(maxH))
				if h == 0 {
					h = 1
				}
				w.Heights[i] = h
				w.Burns[i] = 0
			}
		}
	}
}

func (w *World) parallelUpdate() {
	switch w.cfg.Scheduler {
	case SchedulerDynamicChunks:
		w.updateDynamicChunks()

	case SchedulerStaticStripes:
		// TODO: реализовать статические полосы.
		w.updateDynamicChunks()

	case SchedulerMainSMT:
		// TODO: реализовать основные чанки + SMT-подчанки.
		w.updateDynamicChunks()

	default:
		w.updateDynamicChunks()
	}
}

func (w *World) updateDynamicChunks() {
	if len(w.chunks) == 0 {
		return
	}

	jobs := make(chan int, len(w.chunks))
	for i := range w.chunks {
		jobs <- i
	}
	close(jobs)

	var wg sync.WaitGroup

	workers := w.cfg.WorkerCount
	if workers <= 0 {
		workers = runtime.NumCPU()
	}

	for i := 0; i < workers; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for idx := range jobs {
				w.updateChunk(w.chunks[idx])
			}
		}()
	}

	wg.Wait()
}

func (w *World) buildDynamicChunks() {
	cs := w.cfg.ChunkSize
	if cs <= 0 {
		cs = 64
	}

	chunkCountX := (w.Width + cs - 1) / cs
	chunkCountY := (w.Height + cs - 1) / cs

	w.chunks = make([]Chunk, 0, chunkCountX*chunkCountY)

	id := 0

	for cy := 0; cy < w.Height; cy += cs {
		y0 := cy + 1
		y1 := cy + cs
		if y1 > w.Height {
			y1 = w.Height
		}
		y1++ // exclusive

		for cx := 0; cx < w.Width; cx += cs {
			x0 := cx + 1
			x1 := cx + cs
			if x1 > w.Width {
				x1 = w.Width
			}
			x1++ // exclusive

			w.chunks = append(w.chunks, Chunk{
				ID: id,
				X0: x0,
				Y0: y0,
				X1: x1,
				Y1: y1,
			})

			id++
		}
	}
}

func (w *World) chunkRNG(chunkID int) rng {
	seed := w.cfg.Seed ^
		(w.tick * 0x9E3779B97F4A7C15) ^
		(uint64(chunkID) * 0xBF58476D1CE4E5B9)

	r := rng(seed)

	// Прогрев.
	r.next()
	r.next()

	return r
}

func (w *World) updateChunk(c Chunk) {
	r := w.chunkRNG(c.ID)

	for y := c.Y0; y < c.Y1; y++ {
		i := y*w.Stride + c.X0

		for x := c.X0; x < c.X1; x++ {
			h := w.Heights[i]
			b := w.Burns[i]

			var nextH uint8
			var nextB uint8

			switch {
			case b > 0:
				// Горящее дерево.
				if b == 1 {
					nextH = 0
					nextB = 0
				} else {
					nextH = h
					nextB = b - 1
				}

			case h == 0:
				// Пустая клетка.
				if r.next() < w.spawnThreshold {
					nextH = 1
					nextB = 0
				} else {
					nextH = 0
					nextB = 0
				}

			default:
				// Живое дерево.
				weight := w.fireWeight(i)

				if weight > 0 && r.next() < w.fireThreshold[weight] {
					nextH = h
					nextB = w.burnDuration[h]
				} else if h < w.cfg.Growth.MaxHeight && r.next() < w.growThreshold {
					nextH = h + 1
					nextB = 0
				} else {
					nextH = h
					nextB = 0
				}
			}

			w.NextHeights[i] = nextH
			w.NextBurns[i] = nextB

			i++
		}
	}
}

func (w *World) fireWeight(i int) int {
	radius := w.cfg.Fire.SpreadRadius
	if radius <= 0 {
		radius = 1
	}

	s := w.Stride
	weight := 0

	// Получаем координаты текущей клетки
	x := i % s
	y := i / s

	// Перебираем все клетки в радиусе
	for dy := -radius; dy <= radius; dy++ {
		for dx := -radius; dx <= radius; dx++ {
			if dx == 0 && dy == 0 {
				continue // пропускаем саму клетку
			}

			// Проверяем границы
			nx := x + dx
			ny := y + dy

			// Пустая граница имеет координаты 0 и stride-1 для x
			// и 0 и Height+1 для y
			if nx <= 0 || nx >= s-1 || ny <= 0 || ny > w.Height {
				continue
			}

			neighborIdx := i + dy*s + dx

			if w.Burns[neighborIdx] > 0 {
				// Определяем вес в зависимости от расстояния
				if dx == 0 || dy == 0 {
					// Ортогональный сосед
					weight += w.orthoWeight
				} else if abs(dx) == 1 && abs(dy) == 1 {
					// Диагональный сосед на расстоянии 1
					weight += w.diagWeight
				} else {
					// Дальние соседи - уменьшенный вес
					weight += w.diagWeight / 2
				}
			}
		}
	}

	return weight
}

func (w *World) applyLightning(dt float64) {
	lambda := w.cfg.Lightning.StrikesPerSecond * dt
	strikes := poisson(lambda, &w.rng)

	if strikes == 0 {
		return
	}

	radius := w.cfg.Lightning.Radius
	if radius < 0 {
		radius = 0
	}

	for k := 0; k < strikes; k++ {
		cx := 1 + w.rng.intN(w.Width)
		cy := 1 + w.rng.intN(w.Height)

		minX := cx - radius
		if minX < 1 {
			minX = 1
		}

		maxX := cx + radius
		if maxX > w.Width {
			maxX = w.Width
		}

		minY := cy - radius
		if minY < 1 {
			minY = 1
		}

		maxY := cy + radius
		if maxY > w.Height {
			maxY = w.Height
		}

		best := -1
		var bestH uint8

		for y := minY; y <= maxY; y++ {
			base := y * w.Stride

			for x := minX; x <= maxX; x++ {
				i := base + x

				// Молния выбирает живое дерево, а не уже горящее.
				if w.Burns[i] == 0 && w.Heights[i] > bestH {
					bestH = w.Heights[i]
					best = i
				}
			}
		}

		if best >= 0 && bestH > 0 {
			// Если молния попала в дерево, оно загорается всегда.
			w.Burns[best] = w.burnDuration[bestH]
		}
	}
}

func poisson(lambda float64, r *rng) int {
	if lambda <= 0 {
		return 0
	}

	// Для больших lambda пока делаем грубо.
	// Для симуляции это нормально, но потом можно улучшить.
	if lambda > 30 {
		return int(lambda)
	}

	L := math.Exp(-lambda)
	k := 0
	p := 1.0

	for {
		p *= r.float64()
		if p <= L {
			return k
		}
		k++
	}
}

func rateThreshold(ratePerSecond float64, dt float64) uint64 {
	if ratePerSecond <= 0 {
		return 0
	}

	p := 1.0 - math.Exp(-ratePerSecond*dt)
	return probabilityThreshold(p)
}

func probabilityThreshold(p float64) uint64 {
	if p <= 0 {
		return 0
	}
	if p >= 1 {
		return math.MaxUint64
	}

	return uint64(p * float64(math.MaxUint64))
}

// Config возвращает конфигурацию мира.
func (w *World) Config() Config {
	return w.cfg
}

// TickRate возвращает количество тиков в секунду.
func (w *World) TickRate() int {
	return w.cfg.TickRate
}

// FireConfig возвращает конфиг пожара.
func (w *World) FireConfig() FireConfig {
	return w.cfg.Fire
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
