package main

import (
	"log"

	"github.com/aizonwork/treeburnsimulation/render"
	"github.com/aizonwork/treeburnsimulation/sim"
	"github.com/hajimehoshi/ebiten/v2"
)

func main() {
	cfg := sim.DefaultConfig()

	cfg.Width = 1000
	cfg.Height = 1000
	cfg.TickRate = 160
	cfg.WorkerCount = 12
	cfg.Scheduler = sim.SchedulerDynamicChunks
	cfg.Growth.SpawnRatePerSecond = 0.0005 // было 0.000005
	cfg.Growth.GrowRatePerSecond = 2.      // было 0.02
	cfg.Fire.SpreadRadius = 2
	cfg.Fire.BurnTicksPerHeight = 0.35

	world := sim.NewWorld(cfg)

	// Заполняем лесом (можно уменьшить density для разреженного леса)
	world.FillTrees(0.5)

	game := render.NewGame(world)

	ebiten.SetWindowSize(1200, 800)
	ebiten.SetWindowTitle("Forest Fire Simulation")
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)
	ebiten.SetVsyncEnabled(true)

	if err := ebiten.RunGame(game); err != nil {
		log.Fatal(err)
	}
}
