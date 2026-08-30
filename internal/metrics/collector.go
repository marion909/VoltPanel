// Package metrics sammelt die Serverwerte für das Dashboard und verteilt sie
// an alle verbundenen Browser.
package metrics

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
)

// Snapshot ist ein Messpunkt, wie ihn das Dashboard bekommt.
type Snapshot struct {
	Timestamp int64 `json:"timestamp"`

	CPUPercent  float64   `json:"cpu_percent"`
	CPUPerCore  []float64 `json:"cpu_per_core"`
	CPUCores    int       `json:"cpu_cores"`
	LoadAvg1    float64   `json:"load_1"`
	LoadAvg5    float64   `json:"load_5"`
	LoadAvg15   float64   `json:"load_15"`
	LoadPercent float64   `json:"load_percent"`

	MemTotal   uint64  `json:"mem_total"`
	MemUsed    uint64  `json:"mem_used"`
	MemPercent float64 `json:"mem_percent"`

	SwapTotal   uint64  `json:"swap_total"`
	SwapUsed    uint64  `json:"swap_used"`
	SwapPercent float64 `json:"swap_percent"`

	Disks []DiskUsage `json:"disks"`

	NetRxBytes    uint64  `json:"net_rx_bytes"`
	NetTxBytes    uint64  `json:"net_tx_bytes"`
	NetRxPerSec   float64 `json:"net_rx_per_sec"`
	NetTxPerSec   float64 `json:"net_tx_per_sec"`
	Uptime        uint64  `json:"uptime"`
	ProcessCount  int     `json:"process_count"`
	BootTimestamp uint64  `json:"boot_time"`
}

type DiskUsage struct {
	Mountpoint string  `json:"mountpoint"`
	Device     string  `json:"device"`
	Fstype     string  `json:"fstype"`
	Total      uint64  `json:"total"`
	Used       uint64  `json:"used"`
	Free       uint64  `json:"free"`
	Percent    float64 `json:"percent"`
}

// Collector misst periodisch und hält die letzten Werte für neue Verbindungen
// vor, damit ein frisch geöffnetes Dashboard nicht erst ein Intervall lang leer bleibt.
type Collector struct {
	interval time.Duration
	log      *slog.Logger
	history  int

	mu       sync.RWMutex
	latest   Snapshot
	series   []Snapshot
	subs     map[chan Snapshot]struct{}
	lastNet  net.IOCountersStat
	lastTime time.Time
}

func New(interval time.Duration, logger *slog.Logger) *Collector {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Collector{
		interval: interval,
		log:      logger,
		history:  120, // bei 2s Intervall die letzten vier Minuten
		subs:     make(map[chan Snapshot]struct{}),
	}
}

// Run misst, bis der Context endet.
func (c *Collector) Run(ctx context.Context) {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	c.collect(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.collect(ctx)
		}
	}
}

func (c *Collector) Latest() Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.latest
}

// Series liefert den Verlauf für den Traffic-Chart beim Seitenaufbau.
func (c *Collector) Series() []Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]Snapshot, len(c.series))
	copy(out, c.series)
	return out
}

// Subscribe meldet einen Empfänger an. Der zurückgegebene Kanal ist gepuffert;
// ein Empfänger, der nicht mitkommt, verliert Messpunkte, statt den Collector
// auszubremsen.
func (c *Collector) Subscribe() (<-chan Snapshot, func()) {
	ch := make(chan Snapshot, 8)
	c.mu.Lock()
	c.subs[ch] = struct{}{}
	c.mu.Unlock()

	return ch, func() {
		c.mu.Lock()
		if _, ok := c.subs[ch]; ok {
			delete(c.subs, ch)
			close(ch)
		}
		c.mu.Unlock()
	}
}

func (c *Collector) collect(ctx context.Context) {
	snap := Snapshot{Timestamp: time.Now().Unix()}

	if percents, err := cpu.PercentWithContext(ctx, 0, true); err == nil && len(percents) > 0 {
		snap.CPUPerCore = percents
		snap.CPUCores = len(percents)
		var sum float64
		for _, p := range percents {
			sum += p
		}
		snap.CPUPercent = sum / float64(len(percents))
	}

	if avg, err := load.AvgWithContext(ctx); err == nil {
		snap.LoadAvg1, snap.LoadAvg5, snap.LoadAvg15 = avg.Load1, avg.Load5, avg.Load15
		if snap.CPUCores > 0 {
			// Load im Verhältnis zu den Kernen — erst so ist der Wert vergleichbar.
			snap.LoadPercent = min(avg.Load1/float64(snap.CPUCores)*100, 100)
		}
	}

	if vm, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		snap.MemTotal, snap.MemUsed, snap.MemPercent = vm.Total, vm.Used, vm.UsedPercent
	}
	if sm, err := mem.SwapMemoryWithContext(ctx); err == nil {
		snap.SwapTotal, snap.SwapUsed, snap.SwapPercent = sm.Total, sm.Used, sm.UsedPercent
	}

	snap.Disks = collectDisks(ctx)

	if counters, err := net.IOCountersWithContext(ctx, false); err == nil && len(counters) > 0 {
		snap.NetRxBytes, snap.NetTxBytes = counters[0].BytesRecv, counters[0].BytesSent

		// Erste Messung hat keinen Vorgänger: Rate bleibt 0 statt eines
		// Ausreißers in Höhe des gesamten Zählerstands seit dem Systemstart.
		c.mu.RLock()
		last, lastTime := c.lastNet, c.lastTime
		c.mu.RUnlock()

		if !lastTime.IsZero() {
			if elapsed := time.Since(lastTime).Seconds(); elapsed > 0 {
				snap.NetRxPerSec = deltaRate(counters[0].BytesRecv, last.BytesRecv, elapsed)
				snap.NetTxPerSec = deltaRate(counters[0].BytesSent, last.BytesSent, elapsed)
			}
		}
		c.mu.Lock()
		c.lastNet, c.lastTime = counters[0], time.Now()
		c.mu.Unlock()
	}

	if info, err := host.InfoWithContext(ctx); err == nil {
		snap.Uptime, snap.BootTimestamp = info.Uptime, info.BootTime
		snap.ProcessCount = int(info.Procs)
	}

	c.publish(snap)
}

// deltaRate rechnet die Differenz zweier Zählerstände in Bytes pro Sekunde um.
// Ein Zählerüberlauf oder ein Interface-Reset ergäbe einen negativen Wert —
// dann lieber 0 melden als einen unsinnigen Ausschlag im Chart.
func deltaRate(current, previous uint64, seconds float64) float64 {
	if current < previous {
		return 0
	}
	return float64(current-previous) / seconds
}

func collectDisks(ctx context.Context) []DiskUsage {
	partitions, err := disk.PartitionsWithContext(ctx, false)
	if err != nil {
		return nil
	}

	out := make([]DiskUsage, 0, len(partitions))
	for _, p := range partitions {
		// Pseudo-Dateisysteme sagen nichts über den freien Plattenplatz aus.
		switch p.Fstype {
		case "tmpfs", "devtmpfs", "overlay", "squashfs", "ramfs", "autofs", "devfs":
			continue
		}
		usage, err := disk.UsageWithContext(ctx, p.Mountpoint)
		if err != nil || usage.Total == 0 {
			continue
		}
		out = append(out, DiskUsage{
			Mountpoint: p.Mountpoint, Device: p.Device, Fstype: p.Fstype,
			Total: usage.Total, Used: usage.Used, Free: usage.Free, Percent: usage.UsedPercent,
		})
	}
	return out
}

func (c *Collector) publish(snap Snapshot) {
	c.mu.Lock()
	c.latest = snap
	c.series = append(c.series, snap)
	if len(c.series) > c.history {
		c.series = c.series[len(c.series)-c.history:]
	}
	subs := make([]chan Snapshot, 0, len(c.subs))
	for ch := range c.subs {
		subs = append(subs, ch)
	}
	c.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- snap:
		default:
			// Empfänger hängt — der nächste Messpunkt kommt in zwei Sekunden.
		}
	}
}
