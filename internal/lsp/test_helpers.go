package lsp

import (
	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/lsp/cache"
)

// Helper functions for testing to maintain backward compatibility
// and allow tests to inspect internal state.

func ResetTestServer() {
	GlobalSession = cache.NewSession("test")
	GlobalSession.CreateView("default", "/")
}

func SetTestProjectRoot(root string) {
	GlobalSession = cache.NewSession("test")
	GlobalSession.CreateView("default", root)
}

func GetTestTree() *index.ProjectTree {
	if GlobalSession == nil {
		ResetTestServer()
	}
	// Return the tree of the first view (usually 'default')
	views := GlobalSession.Views()
	if len(views) > 0 {
		return views[0].Snapshot().Tree()
	}
	return nil
}

func GetTestDocuments() map[string]string {
	if GlobalSession == nil {
		ResetTestServer()
	}
	views := GlobalSession.Views()
	if len(views) > 0 {
		return views[0].Snapshot().Documents()
	}
	return nil
}