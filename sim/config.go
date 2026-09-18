package sim

type SchedulerMode int

const (
	// Простой режим: много маленьких чанков, воркеры берут их из очереди.
	SchedulerDynamicChunks SchedulerMode = iota

	// Будущий режим: статические полосы на воркера.
	SchedulerStaticStripes

	// Будущий режим: большие основные чанки + разделение под SMT.
	SchedulerMainSMT
)

type SaveFormat string

const (
	FormatPNG  SaveFormat = "png"
	FormatJPEG SaveFormat = "jpeg"
)

type SaveConfig struct {
	// Включить автосохранение изображений
	Enabled bool

	// Каждые сколько тиков сохранять
	EveryTicks int

	// Директория для сохранения
	Dir string

	// Префикс имени файла
	Prefix string

	// Количество цифр в имени файла
	FilenameDigits int

	// Писать логи в файл (если false - только в консоль/UI)
	LogToFile bool

	// Framerate для видео (0 = использовать TickRate)
	VideoFramerate int

	// Формат сохранения скриншотов
	Format SaveFormat

	// Качество для JPEG (0-100). Игнорируется для PNG.
	Quality int

	// Адаптивная запись при крупных пожарах
	AdaptiveEnabled bool

	// Порог числа горящих клеток для "крупного пожара"
	// Если 0 - используется процентный порог
	FireThreshold int

	// Процент горящих клеток от общего числа деревьев для "крупного пожара"
	// Используется если FireThreshold = 0
	FirePercentThreshold float64

	// Каждые сколько тиков сохранять при крупном пожаре
	EveryTicksOnFire int
}

type GrowthConfig struct {
	// Появление нового дерева на пустой клетке, событий в секунду.
	SpawnRatePerSecond float64

	// Рост существующего дерева на +1 height, событий в секунду.
	GrowRatePerSecond float64

	MaxHeight uint8
}

type LightningConfig struct {
	// Среднее число ударов молнии в секунду на всю карту.
	StrikesPerSecond float64

	// Радиус поиска самого высокого дерева вокруг точки удара.
	Radius int
}

type FireConfig struct {
	// Базовая скорость распространения огня, событий в секунду.
	SpreadRatePerSecond float64

	// Множитель для диагональных соседей.
	// 1.0 = так же, как ортогональные.
	// 0.7 = слабее.
	DiagonalFactor float64

	// Базовое время горения в тиках.
	BurnBaseTicks uint8

	// Сколько дополнительных тиков горения даёт единица высоты.
	BurnTicksPerHeight float64

	// Радиус распространения огня (1 = только соседние клетки, 2 = через клетку и т.д.)
	SpreadRadius int
}

type Config struct {
	Width  int
	Height int

	// Тиков симуляции в секунду.
	TickRate int

	// Сколько горутин-воркеров использовать.
	WorkerCount int

	// Размер чанка для dynamic режима.
	ChunkSize int

	Scheduler SchedulerMode

	Seed uint64

	Growth    GrowthConfig
	Lightning LightningConfig
	Fire      FireConfig
	Save      SaveConfig
}

// TODO: добавить меню настроек, с возможностью редактировать параметры
func DefaultConfig() Config {
	return Config{
		Width:       1000,
		Height:      1000,
		TickRate:    10,
		WorkerCount: 12,
		ChunkSize:   64,
		Scheduler:   SchedulerDynamicChunks,
		Seed:        42,

		Growth: GrowthConfig{
			SpawnRatePerSecond: 0.000005,
			GrowRatePerSecond:  0.02,
			MaxHeight:          255,
		},

		Lightning: LightningConfig{
			StrikesPerSecond: 0.3,
			Radius:           2,
		},

		Fire: FireConfig{
			SpreadRadius:        1,
			SpreadRatePerSecond: 0.7,
			DiagonalFactor:      0.7,
			BurnBaseTicks:       20,
			BurnTicksPerHeight:  0.5,
		},

		Save: SaveConfig{
			Enabled:              false,
			EveryTicks:           100, // каждые 100 тиков (10 секунд при 10 TPS)
			Dir:                  "screenshots",
			Prefix:               "sim",
			FilenameDigits:       4,
			LogToFile:            false,
			VideoFramerate:       60,
			Format:               FormatJPEG,
			Quality:              85,
			AdaptiveEnabled:      true,
			FireThreshold:        100,
			FirePercentThreshold: 0.1,
			EveryTicksOnFire:     160,
		},
	}
}
