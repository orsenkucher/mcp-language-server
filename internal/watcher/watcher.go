package watcher

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/isaacphi/mcp-language-server/internal/lsp"
	"github.com/isaacphi/mcp-language-server/internal/protocol"
	gitignore "github.com/sabhiram/go-gitignore"
)

var debug = true // Force debug logging on

// WorkspaceWatcher manages LSP file watching
type WorkspaceWatcher struct {
	client        *lsp.Client
	workspacePath string
	gitIgnore     *gitignore.GitIgnore

	debounceTime time.Duration
	debounceMap  map[string]*time.Timer
	debounceMu   sync.Mutex

	// File watchers registered by the server
	registrations  []protocol.FileSystemWatcher
	registrationMu sync.RWMutex
}

// NewWorkspaceWatcher creates a new workspace watcher
func NewWorkspaceWatcher(client *lsp.Client) *WorkspaceWatcher {
	return &WorkspaceWatcher{
		client:        client,
		debounceTime:  300 * time.Millisecond,
		debounceMap:   make(map[string]*time.Timer),
		registrations: []protocol.FileSystemWatcher{},
	}
}

// AddRegistrations adds file watchers to track
func (w *WorkspaceWatcher) AddRegistrations(ctx context.Context, id string, watchers []protocol.FileSystemWatcher) {
	w.registrationMu.Lock()
	defer w.registrationMu.Unlock()

	// Add new watchers
	w.registrations = append(w.registrations, watchers...)

	// Print detailed registration information for debugging
	if debug {
		log.Printf("Added %d file watcher registrations (id: %s), total: %d",
			len(watchers), id, len(w.registrations))

		for i, watcher := range watchers {
			log.Printf("Registration #%d raw data:", i+1)

			// Log the GlobPattern
			switch v := watcher.GlobPattern.Value.(type) {
			case string:
				log.Printf("  GlobPattern: string pattern '%s'", v)
			case protocol.RelativePattern:
				log.Printf("  GlobPattern: RelativePattern with pattern '%s'", v.Pattern)

				// Log BaseURI details
				switch u := v.BaseURI.Value.(type) {
				case string:
					log.Printf("    BaseURI: string '%s'", u)
				case protocol.DocumentUri:
					log.Printf("    BaseURI: DocumentUri '%s'", u)
				default:
					log.Printf("    BaseURI: unknown type %T", u)
				}
			default:
				log.Printf("  GlobPattern: unknown type %T", v)
			}

			// Log WatchKind
			watchKind := protocol.WatchKind(protocol.WatchChange | protocol.WatchCreate | protocol.WatchDelete)
			if watcher.Kind != nil {
				watchKind = *watcher.Kind
			}
			log.Printf("  WatchKind: %d (Create:%v, Change:%v, Delete:%v)",
				watchKind,
				watchKind&protocol.WatchCreate != 0,
				watchKind&protocol.WatchChange != 0,
				watchKind&protocol.WatchDelete != 0)

			// Test match against some example paths
			testPaths := []string{
				"/Users/phil/dev/mcp-language-server/internal/watcher/watcher.go",
				"/Users/phil/dev/mcp-language-server/go.mod",
			}

			for _, testPath := range testPaths {
				isMatch := w.matchesPattern(testPath, watcher.GlobPattern)
				log.Printf("  Test path '%s': %v", testPath, isMatch)
			}
		}
	}

	// Find and open all existing files that match the newly registered patterns
	// TODO: not all language servers require this, but typescript does. Make this configurable
	go func() {
		startTime := time.Now()
		filesOpened := 0

		err := filepath.WalkDir(w.workspacePath, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}

			// Skip directories that should be excluded
			if d.IsDir() {
				if path != w.workspacePath && w.shouldExcludeDir(path) {
					if debug {
						log.Printf("Skipping excluded directory!: %s", path)
					}
					return filepath.SkipDir
				}
			} else {
				// Process files
				w.openMatchingFile(ctx, path)
				filesOpened++

				// Add a small delay after every 100 files to prevent overwhelming the server
				if filesOpened%100 == 0 {
					time.Sleep(10 * time.Millisecond)
				}
			}

			return nil
		})

		elapsedTime := time.Since(startTime)
		if debug {
			log.Printf("Workspace scan complete: processed %d files in %.2f seconds", filesOpened, elapsedTime.Seconds())
		}

		if err != nil && debug {
			log.Printf("Error scanning workspace for files to open: %v", err)
		}
	}()
}

// WatchWorkspace sets up file watching for a workspace
func (w *WorkspaceWatcher) WatchWorkspace(ctx context.Context, workspacePath string) {
	w.workspacePath = workspacePath

	// Load .gitignore
	gitignorePath := filepath.Join(workspacePath, ".gitignore")
	if _, err := os.Stat(gitignorePath); err == nil {
		var compileErr error
		w.gitIgnore, compileErr = gitignore.CompileIgnoreFile(gitignorePath)
		if compileErr != nil {
			log.Printf("Error compiling .gitignore file %s: %v", gitignorePath, compileErr)
			// Continue without gitignore rules if compilation fails
			w.gitIgnore = nil
		} else if debug {
			log.Printf("Successfully loaded .gitignore from %s", gitignorePath)
		}
	} else if !os.IsNotExist(err) {
		log.Printf("Error checking for .gitignore file %s: %v", gitignorePath, err)
	} else if debug {
		log.Printf(".gitignore not found at %s", gitignorePath)
	}

	// Register handler for file watcher registrations from the server
	lsp.RegisterFileWatchHandler(func(id string, watchers []protocol.FileSystemWatcher) {
		w.AddRegistrations(ctx, id, watchers)
	})

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatalf("Error creating watcher: %v", err)
	}
	defer watcher.Close()

	// Watch the workspace recursively
	err = filepath.WalkDir(workspacePath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip excluded directories (except workspace root)
		if d.IsDir() && path != workspacePath {
			if w.shouldExcludeDir(path) {
				if debug {
					log.Printf("Skipping watching excluded directory: %s", path)
				}
				return filepath.SkipDir
			}
		}

		// Add directories to watcher
		if d.IsDir() {
			err = watcher.Add(path)
			if err != nil {
				log.Printf("Error watching path %s: %v", path, err)
			}
		}

		return nil
	})

	if err != nil {
		log.Fatalf("Error walking workspace: %v", err)
	}

	// Event loop
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}

			uri := fmt.Sprintf("file://%s", event.Name)

			// Add new directories to the watcher
			if event.Op&fsnotify.Create != 0 {
				if info, err := os.Stat(event.Name); err == nil {
					if info.IsDir() {
						// Skip excluded directories
						if !w.shouldExcludeDir(event.Name) {
							if err := watcher.Add(event.Name); err != nil {
								log.Printf("Error watching new directory: %v", err)
							}
						}
					} else {
						// For newly created files
						if !w.shouldExcludeFile(event.Name) {
							w.openMatchingFile(ctx, event.Name)
						}
					}
				}
			}

			// Debug logging
			if debug {
				matched, kind := w.isPathWatched(event.Name)
				log.Printf("Event: %s, Op: %s, Watched: %v, Kind: %d",
					event.Name, event.Op.String(), matched, kind)
			}

			// Check if this path should be watched according to server registrations
			if watched, watchKind := w.isPathWatched(event.Name); watched {
				switch {
				case event.Op&fsnotify.Write != 0:
					if watchKind&protocol.WatchChange != 0 {
						w.debounceHandleFileEvent(ctx, uri, protocol.FileChangeType(protocol.Changed))
					}
				case event.Op&fsnotify.Create != 0:
					// Already handled earlier in the event loop
					// Just send the notification if needed
					info, _ := os.Stat(event.Name)
					if info != nil && !info.IsDir() && watchKind&protocol.WatchCreate != 0 && !w.shouldExcludeFile(event.Name) {
						w.debounceHandleFileEvent(ctx, uri, protocol.FileChangeType(protocol.Created))
					}
				case event.Op&fsnotify.Remove != 0:
					if watchKind&protocol.WatchDelete != 0 && !w.shouldExcludeFile(event.Name) {
						w.handleFileEvent(ctx, uri, protocol.FileChangeType(protocol.Deleted))
					}
				case event.Op&fsnotify.Rename != 0:
					// For renames, first delete if not excluded
					if watchKind&protocol.WatchDelete != 0 && !w.shouldExcludeFile(event.Name) {
						w.handleFileEvent(ctx, uri, protocol.FileChangeType(protocol.Deleted))
					}

					// Then check if the new file exists and create an event if not excluded
					if info, err := os.Stat(event.Name); err == nil && !info.IsDir() {
						if watchKind&protocol.WatchCreate != 0 && !w.shouldExcludeFile(event.Name) {
							w.debounceHandleFileEvent(ctx, uri, protocol.FileChangeType(protocol.Created))
						}
					}
				}
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			log.Printf("Watcher error: %v\n", err)
		}
	}
}

// isPathWatched checks if a path should be watched based on server registrations
func (w *WorkspaceWatcher) isPathWatched(path string) (bool, protocol.WatchKind) {
	w.registrationMu.RLock()
	defer w.registrationMu.RUnlock()

	// If no explicit registrations, watch everything
	if len(w.registrations) == 0 {
		return true, protocol.WatchKind(protocol.WatchChange | protocol.WatchCreate | protocol.WatchDelete)
	}

	// Check each registration
	for _, reg := range w.registrations {
		isMatch := w.matchesPattern(path, reg.GlobPattern)
		if isMatch {
			kind := protocol.WatchKind(protocol.WatchChange | protocol.WatchCreate | protocol.WatchDelete)
			if reg.Kind != nil {
				kind = *reg.Kind
			}
			return true, kind
		}
	}

	return false, 0
}

// matchesGlob handles advanced glob patterns including ** and alternatives
func matchesGlob(pattern, path string) bool {
	// Handle file extension patterns with braces like *.{go,mod,sum}
	if strings.Contains(pattern, "{") && strings.Contains(pattern, "}") {
		// Extract extensions from pattern like "*.{go,mod,sum}"
		parts := strings.SplitN(pattern, "{", 2)
		if len(parts) == 2 {
			prefix := parts[0]
			extPart := strings.SplitN(parts[1], "}", 2)
			if len(extPart) == 2 {
				extensions := strings.Split(extPart[0], ",")
				suffix := extPart[1]

				// Check if the path matches any of the extensions
				for _, ext := range extensions {
					extPattern := prefix + ext + suffix
					isMatch := matchesSimpleGlob(extPattern, path)
					if isMatch {
						return true
					}
				}
				return false
			}
		}
	}

	return matchesSimpleGlob(pattern, path)
}

// matchesSimpleGlob handles glob patterns with ** wildcards
func matchesSimpleGlob(pattern, path string) bool {
	// Handle special case for **/*.ext pattern (common in LSP)
	if strings.HasPrefix(pattern, "**/") {
		rest := strings.TrimPrefix(pattern, "**/")

		// If the rest is a simple file extension pattern like *.go
		if strings.HasPrefix(rest, "*.") {
			ext := strings.TrimPrefix(rest, "*")
			isMatch := strings.HasSuffix(path, ext)
			return isMatch
		}

		// Otherwise, try to check if the path ends with the rest part
		isMatch := strings.HasSuffix(path, rest)

		// If it matches directly, great!
		if isMatch {
			return true
		}

		// Otherwise, check if any path component matches
		pathComponents := strings.Split(path, "/")
		for i := 0; i < len(pathComponents); i++ {
			subPath := strings.Join(pathComponents[i:], "/")
			if strings.HasSuffix(subPath, rest) {
				return true
			}
		}

		return false
	}

	// Handle other ** wildcard pattern cases
	if strings.Contains(pattern, "**") {
		parts := strings.Split(pattern, "**")

		// Validate the path starts with the first part
		if !strings.HasPrefix(path, parts[0]) && parts[0] != "" {
			return false
		}

		// For patterns like "**/*.go", just check the suffix
		if len(parts) == 2 && parts[0] == "" {
			isMatch := strings.HasSuffix(path, parts[1])
			return isMatch
		}

		// For other patterns, handle middle part
		remaining := strings.TrimPrefix(path, parts[0])
		if len(parts) == 2 {
			isMatch := strings.HasSuffix(remaining, parts[1])
			return isMatch
		}
	}

	// Handle simple * wildcard for file extension patterns (*.go, *.sum, etc)
	if strings.HasPrefix(pattern, "*.") {
		ext := strings.TrimPrefix(pattern, "*")
		isMatch := strings.HasSuffix(path, ext)
		return isMatch
	}

	// Fall back to simple matching for simpler patterns
	matched, err := filepath.Match(pattern, path)
	if err != nil {
		log.Printf("Error matching pattern %s: %v", pattern, err)
		return false
	}

	return matched
}

// matchesPattern checks if a path matches the glob pattern
func (w *WorkspaceWatcher) matchesPattern(path string, pattern protocol.GlobPattern) bool {
	patternInfo, err := pattern.AsPattern()
	if err != nil {
		log.Printf("Error parsing pattern: %v", err)
		return false
	}

	basePath := patternInfo.GetBasePath()
	patternText := patternInfo.GetPattern()

	path = filepath.ToSlash(path)

	// For simple patterns without base path
	if basePath == "" {
		// Check if the pattern matches the full path or just the file extension
		fullPathMatch := matchesGlob(patternText, path)
		baseNameMatch := matchesGlob(patternText, filepath.Base(path))

		return fullPathMatch || baseNameMatch
	}

	// For relative patterns
	basePath = strings.TrimPrefix(basePath, "file://")
	basePath = filepath.ToSlash(basePath)

	// Make path relative to basePath for matching
	relPath, err := filepath.Rel(basePath, path)
	if err != nil {
		log.Printf("Error getting relative path for %s: %v", path, err)
		return false
	}
	relPath = filepath.ToSlash(relPath)

	isMatch := matchesGlob(patternText, relPath)

	return isMatch
}

// debounceHandleFileEvent handles file events with debouncing to reduce notifications
func (w *WorkspaceWatcher) debounceHandleFileEvent(ctx context.Context, uri string, changeType protocol.FileChangeType) {
	w.debounceMu.Lock()
	defer w.debounceMu.Unlock()

	// Create a unique key based on URI and change type
	key := fmt.Sprintf("%s:%d", uri, changeType)

	// Cancel existing timer if any
	if timer, exists := w.debounceMap[key]; exists {
		timer.Stop()
	}

	// Create new timer
	w.debounceMap[key] = time.AfterFunc(w.debounceTime, func() {
		w.handleFileEvent(ctx, uri, changeType)

		// Cleanup timer after execution
		w.debounceMu.Lock()
		delete(w.debounceMap, key)
		w.debounceMu.Unlock()
	})
}

// handleFileEvent sends file change notifications
func (w *WorkspaceWatcher) handleFileEvent(ctx context.Context, uri string, changeType protocol.FileChangeType) {
	// If the file is open and it's a change event, use didChange notification
	filePath := uri[7:] // Remove "file://" prefix
	if changeType == protocol.FileChangeType(protocol.Changed) && w.client.IsFileOpen(filePath) {
		err := w.client.NotifyChange(ctx, filePath)
		if err != nil {
			log.Printf("Error notifying change: %v", err)
		}
		return
	}

	// Notify LSP server about the file event using didChangeWatchedFiles
	if err := w.notifyFileEvent(ctx, uri, changeType); err != nil {
		log.Printf("Error notifying LSP server about file event: %v", err)
	}
}

// notifyFileEvent sends a didChangeWatchedFiles notification for a file event
func (w *WorkspaceWatcher) notifyFileEvent(ctx context.Context, uri string, changeType protocol.FileChangeType) error {
	if debug {
		log.Printf("Notifying file event: %s (type: %d)", uri, changeType)
	}

	params := protocol.DidChangeWatchedFilesParams{
		Changes: []protocol.FileEvent{
			{
				URI:  protocol.DocumentUri(uri),
				Type: changeType,
			},
		},
	}

	return w.client.DidChangeWatchedFiles(ctx, params)
}

// Common patterns for directories and files to exclude
// TODO: make configurable
var (
	excludedDirNames = map[string]bool{
		".git":         true,
		"node_modules": true,
		"dist":         true,
		"build":        true,
		"out":          true,
		"bin":          true,
		".idea":        true,
		".vscode":      true,
		".cache":       true,
		"coverage":     true,
		"target":       true, // Rust build output
		"vendor":       true, // Go vendor directory
	}

	excludedFileExtensions = map[string]bool{
		".swp":   true,
		".swo":   true,
		".tmp":   true,
		".temp":  true,
		".bak":   true,
		".log":   true,
		".o":     true, // Object files
		".so":    true, // Shared libraries
		".dylib": true, // macOS shared libraries
		".dll":   true, // Windows shared libraries
		".a":     true, // Static libraries
		".exe":   true, // Windows executables
		".lock":  true, // Lock files
	}

	// Large binary files that shouldn't be opened
	largeBinaryExtensions = map[string]bool{
		".png":  true,
		".jpg":  true,
		".jpeg": true,
		".gif":  true,
		".bmp":  true,
		".ico":  true,
		".zip":  true,
		".tar":  true,
		".gz":   true,
		".rar":  true,
		".7z":   true,
		".pdf":  true,
		".mp3":  true,
		".mp4":  true,
		".mov":  true,
		".wav":  true,
		".wasm": true,
	}

	// Maximum file size to open (5MB)
	maxFileSize int64 = 5 * 1024 * 1024
)

// shouldExcludeDir returns true if the directory should be excluded from watching/opening
func (w *WorkspaceWatcher) shouldExcludeDir(dirPath string) bool {
	// Check gitignore first
	relPath, err := filepath.Rel(w.workspacePath, dirPath)
	if err == nil && w.gitIgnore != nil && w.gitIgnore.MatchesPath(filepath.ToSlash(relPath)) {
		return true
	}

	dirName := filepath.Base(dirPath)

	// Skip dot directories (common convention, often covered by gitignore but good fallback)
	if strings.HasPrefix(dirName, ".") && dirName != "." && dirName != ".." {
		return true
	}

	// Skip common excluded directories
	if excludedDirNames[dirName] {
		return true
	}

	return false
}

// shouldExcludeFile returns true if the file should be excluded from opening
func (w *WorkspaceWatcher) shouldExcludeFile(filePath string) bool {
	// Check gitignore first
	relPath, err := filepath.Rel(w.workspacePath, filePath)
	if err == nil && w.gitIgnore != nil && w.gitIgnore.MatchesPath(filepath.ToSlash(relPath)) {
		return true
	}

	fileName := filepath.Base(filePath)

	// Skip dot files (common convention, often covered by gitignore but good fallback)
	if strings.HasPrefix(fileName, ".") && fileName != "." && fileName != ".." {
		return true
	}

	// Check file extension
	ext := strings.ToLower(filepath.Ext(filePath))
	if excludedFileExtensions[ext] || largeBinaryExtensions[ext] {
		return true
	}

	// Skip temporary files
	if strings.HasSuffix(filePath, "~") {
		return true
	}

	// Check file size
	info, err := os.Stat(filePath)
	if err != nil {
		// If we can't stat the file, skip it
		return true
	}

	// Skip large files
	if info.Size() > maxFileSize {
		if debug {
			log.Printf("Skipping large file: %s (%.2f MB)", filePath, float64(info.Size())/(1024*1024))
		}
		return true
	}

	return false
}

// openMatchingFile opens a file if it matches any of the registered patterns
func (w *WorkspaceWatcher) openMatchingFile(ctx context.Context, path string) {
	// Skip directories
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return
	}

	// Skip excluded files
	if w.shouldExcludeFile(path) {
		return
	}

	// Check if this path should be watched according to server registrations
	if watched, _ := w.isPathWatched(path); watched {
		// Don't need to check if it's already open - the client.OpenFile handles that
		if err := w.client.OpenFile(ctx, path); err != nil && debug {
			log.Printf("Error opening file %s: %v", path, err)
		}
	}
}
