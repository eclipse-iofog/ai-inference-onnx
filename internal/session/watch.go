//go:build cgo

package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/eclipse-iofog/ai-inference-onnx/internal/discover"
	"github.com/fsnotify/fsnotify"
)

// Watcher reports changes under the model and knowledge roots and reloads the model those files belong to.
type Watcher struct {
	cfg    Config
	repo   *Repository
	poll   time.Duration
	quiet  time.Duration
	fsw    *fsnotify.Watcher
	ctx    context.Context
	cancel context.CancelFunc
	kicks  chan string
	scanMu sync.Mutex
	wg     sync.WaitGroup
}

// Watch reports directory changes and reloads a model after the quiet period.
// pollPeriod is how often identity is compared. quietPeriod is how long to wait after the last change for one model.
// A zero duration uses PollPeriod or QuietPeriod.
// Events for one model collapse into one load. Changes that arrive while that load runs are loaded once more, after the quiet period.
func Watch(ctx context.Context, repo *Repository, cfg Config, pollPeriod, quietPeriod time.Duration) (*Watcher, error) {
	if ctx == nil {
		return nil, errors.New("context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if repo == nil {
		return nil, errors.New("repository is required")
	}
	if strings.TrimSpace(cfg.ModelsRoot) == "" {
		return nil, errors.New("models root is required")
	}
	if pollPeriod <= 0 {
		pollPeriod = PollPeriod
	}
	if quietPeriod <= 0 {
		quietPeriod = QuietPeriod
	}
	cfg.ModelsRoot = filepath.Clean(cfg.ModelsRoot)
	if cfg.KnowledgeRoot != "" {
		cfg.KnowledgeRoot = filepath.Clean(cfg.KnowledgeRoot)
	}
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	w := &Watcher{
		cfg:    cfg,
		repo:   repo,
		poll:   pollPeriod,
		quiet:  quietPeriod,
		fsw:    fsw,
		ctx:    ctx,
		cancel: cancel,
		kicks:  make(chan string, 256),
	}
	if err = fsw.Add(cfg.ModelsRoot); err != nil {
		cancel()
		_ = fsw.Close()
		return nil, fmt.Errorf("watch models: %w", err)
	}
	if cfg.KnowledgeRoot != "" {
		if err = fsw.Add(cfg.KnowledgeRoot); err != nil {
			cancel()
			_ = fsw.Close()
			return nil, fmt.Errorf("watch knowledge: %w", err)
		}
	}
	w.rewatch()
	w.wg.Add(2)
	go w.loop()
	go w.readEvents()
	return w, nil
}

// Scan compares file identity immediately and loads models whose files changed.
func (w *Watcher) Scan() error {
	if w == nil {
		return errors.New("watcher is closed")
	}
	w.scanMu.Lock()
	defer w.scanMu.Unlock()
	return w.syncAll()
}

// Notify schedules a load of one model after the quiet period.
// An empty name schedules every model. Further events for that model during the wait collapse into the same load.
func (w *Watcher) Notify(name string) {
	if name == "" {
		name = allModels
	}
	w.kick(name)
}

// Close stops watching. A load that has already started finishes.
func (w *Watcher) Close() error {
	if w == nil {
		return nil
	}
	w.cancel()
	w.wg.Wait()
	return w.fsw.Close()
}

func (w *Watcher) loop() {
	defer w.wg.Done()
	ticker := time.NewTicker(w.poll)
	defer ticker.Stop()

	var (
		timer  *time.Timer
		timerC <-chan time.Time
	)
	stopTimer := func() {
		if timer == nil {
			return
		}
		if !timer.Stop() {
			select {
			case <-timerC:
			default:
			}
		}
	}
	defer stopTimer()

	pending := make(map[string]time.Time)
	arm := func() {
		stopTimer()
		timer = nil
		timerC = nil
		var next time.Time
		for _, due := range pending {
			if next.IsZero() || due.Before(next) {
				next = due
			}
		}
		if next.IsZero() {
			return
		}
		wait := time.Until(next)
		if wait < 0 {
			wait = 0
		}
		timer = time.NewTimer(wait)
		timerC = timer.C
	}

	for {
		select {
		case <-w.ctx.Done():
			return
		case name := <-w.kicks:
			pending[name] = time.Now().Add(w.quiet)
			arm()
		case <-timerC:
			timer = nil
			timerC = nil
			now := time.Now()
			var due []string
			for name, at := range pending {
				if !now.Before(at) {
					due = append(due, name)
				}
			}
			for _, name := range due {
				delete(pending, name)
			}
			for _, name := range due {
				w.syncName(name)
				w.absorb(pending)
			}
			arm()
		case <-ticker.C:
			w.syncLocked()
			w.absorb(pending)
			arm()
		}
	}
}

func (w *Watcher) absorb(pending map[string]time.Time) {
	for {
		select {
		case name := <-w.kicks:
			pending[name] = time.Now().Add(w.quiet)
		default:
			return
		}
	}
}

func (w *Watcher) readEvents() {
	defer w.wg.Done()
	for {
		select {
		case <-w.ctx.Done():
			return
		case _, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
		case ev, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			w.notePath(ev.Name)
		}
	}
}

func (w *Watcher) notePath(path string) {
	w.watchIfDir(path)
	w.kick(w.owner(path))
}

func (w *Watcher) kick(name string) {
	select {
	case w.kicks <- name:
	case <-w.ctx.Done():
	}
}

func (w *Watcher) owner(path string) string {
	rel, err := filepath.Rel(w.cfg.ModelsRoot, path)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return allModels
	}
	name, _, _ := strings.Cut(rel, string(os.PathSeparator))
	if name == "" || name == "." {
		return allModels
	}
	return name
}

func (w *Watcher) syncLocked() {
	w.scanMu.Lock()
	defer w.scanMu.Unlock()
	_ = w.syncAll()
}

func (w *Watcher) syncName(name string) {
	w.scanMu.Lock()
	defer w.scanMu.Unlock()
	if name == allModels {
		_ = w.syncAll()
		return
	}
	w.syncOne(name)
}

func (w *Watcher) syncAll() error {
	discovered, err := discover.Resolve(w.cfg.ModelsRoot)
	if err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(discovered))
	for _, model := range discovered {
		seen[model.Name] = struct{}{}
		w.repo.apply(w.cfg, model)
	}
	for _, name := range w.repo.modelNames() {
		if _, ok := seen[name]; !ok {
			w.repo.remove(name)
		}
	}
	w.rewatch()
	return nil
}

func (w *Watcher) syncOne(name string) {
	discovered, err := discover.Resolve(w.cfg.ModelsRoot)
	if err != nil {
		return
	}
	for _, model := range discovered {
		if model.Name != name {
			continue
		}
		w.repo.apply(w.cfg, model)
		w.rewatch()
		return
	}
	w.repo.remove(name)
	w.rewatch()
}

func (w *Watcher) rewatch() {
	w.addDir(w.cfg.ModelsRoot)
	w.addTree(w.cfg.ModelsRoot)
	if w.cfg.KnowledgeRoot == "" {
		return
	}
	w.addDir(w.cfg.KnowledgeRoot)
	w.addTree(w.cfg.KnowledgeRoot)
}

func (w *Watcher) addTree(root string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		w.addDir(dir)
		nested, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, child := range nested {
			if child.IsDir() {
				w.addDir(filepath.Join(dir, child.Name()))
			}
		}
	}
}

func (w *Watcher) addDir(path string) {
	w.watchIfDir(path)
}

func (w *Watcher) watchIfDir(path string) {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return
	}
	_ = w.fsw.Add(path)
}
