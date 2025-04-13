package tools

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/isaacphi/mcp-language-server/internal/lsp"
	"github.com/isaacphi/mcp-language-server/internal/protocol"
)

// ScopeIdentifier uniquely identifies a scope (function, method, etc.) in a file
type ScopeIdentifier struct {
	URI       protocol.DocumentUri
	StartLine uint32
	EndLine   uint32
}

// ReferencePosition represents a single reference position within a scope
type ReferencePosition struct {
	Line      uint32
	Character uint32
}

func FindReferences(ctx context.Context, client *lsp.Client, symbolName string, showLineNumbers bool) (string, error) {
	// First get the symbol location like ReadDefinition does
	symbolResult, err := client.Symbol(ctx, protocol.WorkspaceSymbolParams{
		Query: symbolName,
	})
	if err != nil {
		return "", fmt.Errorf("Failed to fetch symbol: %v", err)
	}

	results, err := symbolResult.Results()
	if err != nil {
		return "", fmt.Errorf("Failed to parse results: %v", err)
	}

	var allReferences []string
	totalRefs := 0

	for _, symbol := range results {
		if symbol.GetName() != symbolName {
			continue
		}

		// Get the location of the symbol
		loc := symbol.GetLocation()

		// Use LSP references request with correct params structure
		refsParams := protocol.ReferenceParams{
			TextDocumentPositionParams: protocol.TextDocumentPositionParams{
				TextDocument: protocol.TextDocumentIdentifier{
					URI: loc.URI,
				},
				Position: loc.Range.Start,
			},
			Context: protocol.ReferenceContext{
				IncludeDeclaration: false,
			},
		}

		refs, err := client.References(ctx, refsParams)
		if err != nil {
			return "", fmt.Errorf("Failed to get references: %v", err)
		}

		totalRefs += len(refs)

		// Group references by file first
		refsByFile := make(map[protocol.DocumentUri][]protocol.Location)
		for _, ref := range refs {
			refsByFile[ref.URI] = append(refsByFile[ref.URI], ref)
		}

		// Add summary header
		header := fmt.Sprintf("Symbol: %s (%d references in %d files)",
			symbolName,
			totalRefs,
			len(refsByFile))
		allReferences = append(allReferences, header)

		// Process each file's references
		for uri, fileRefs := range refsByFile {
			filePath := strings.TrimPrefix(string(uri), "file://")
			fileInfo := fmt.Sprintf("\nFile: %s (%d references)\n", filePath, len(fileRefs))
			allReferences = append(allReferences, fileInfo)

			// Group references by scope within each file
			// We'll use ScopeIdentifier to uniquely identify each scope
			scopeRefs := make(map[ScopeIdentifier][]ReferencePosition)
			scopeTexts := make(map[ScopeIdentifier]string)
			scopeNames := make(map[ScopeIdentifier]string)

			// First pass: get scope for each reference
			for _, ref := range fileRefs {
				// Get the full definition/scope containing this reference
				fullScope, scopeLoc, err := GetFullDefinition(ctx, client, ref)
				if err != nil {
					continue
				}

				// Create a scope identifier
				scopeID := ScopeIdentifier{
					URI:       uri,
					StartLine: scopeLoc.Range.Start.Line,
					EndLine:   scopeLoc.Range.End.Line,
				}

				// Add this reference position to the scope
				position := ReferencePosition{
					Line:      ref.Range.Start.Line,
					Character: ref.Range.Start.Character,
				}

				scopeRefs[scopeID] = append(scopeRefs[scopeID], position)
				scopeTexts[scopeID] = fullScope

				// Try to find a name for this scope (only do this once per scope)
				if _, exists := scopeNames[scopeID]; !exists {
					// Extract the first line of the scope to use as a name
					firstLine := strings.Split(fullScope, "\n")[0]
					firstLine = strings.TrimSpace(firstLine)

					// Truncate if too long
					const maxNameLength = 60
					if len(firstLine) > maxNameLength {
						firstLine = firstLine[:maxNameLength] + "..."
					}

					scopeNames[scopeID] = firstLine
				}
			}

			// Second pass: output each scope once with all contained references
			for scopeID, positions := range scopeRefs {
				// Sort positions by line number
				sort.Slice(positions, func(i, j int) bool {
					return positions[i].Line < positions[j].Line ||
						(positions[i].Line == positions[j].Line &&
							positions[i].Character < positions[j].Character)
				})

				// Scope header
				scopeHeader := fmt.Sprintf("  Scope: %s (lines %d-%d, %d references)\n",
					scopeNames[scopeID],
					scopeID.StartLine+1,
					scopeID.EndLine+1,
					len(positions))
				allReferences = append(allReferences, scopeHeader)

				// List reference positions compactly
				var positionStrs []string
				var highlightLines []int // Track which lines to highlight
				for _, pos := range positions {
					positionStrs = append(positionStrs, fmt.Sprintf("L%d:C%d",
						pos.Line+1, pos.Character+1))

					// Calculate the line's position within the scope (for highlighting)
					highlightLines = append(highlightLines, int(pos.Line-scopeID.StartLine))
				}

				// Group the positions into chunks for readability
				const chunkSize = 4
				for i := 0; i < len(positionStrs); i += chunkSize {
					end := i + chunkSize
					if end > len(positionStrs) {
						end = len(positionStrs)
					}
					positionChunk := positionStrs[i:end]
					allReferences = append(allReferences,
						fmt.Sprintf("    References: %s", strings.Join(positionChunk, ", ")))
				}

				// Show the scope content, but minimized and with line numbers if requested
				scopeText := scopeTexts[scopeID]

				// For very large scopes, show just part of it
				lines := strings.Split(scopeText, "\n")
				if len(lines) > 15 {
					// Show beginning and end with ellipsis
					beginning := lines[:7]
					ending := lines[len(lines)-7:]
					lines = append(beginning, "    ...")
					lines = append(lines, ending...)
					scopeText = strings.Join(lines, "\n")
				}

				if showLineNumbers {
					// Use the highlighting version of addLineNumbers for reference lines
					scopeText = addLineNumbers(scopeText, int(scopeID.StartLine)+1, highlightLines...)
				}

				allReferences = append(allReferences, "    "+strings.ReplaceAll(scopeText, "\n", "\n    "))
				allReferences = append(allReferences, "") // Empty line between scopes
			}
		}
	}

	if len(allReferences) == 0 {
		return fmt.Sprintf("No references found for symbol: %s", symbolName), nil
	}

	return strings.Join(allReferences, "\n"), nil
}

// GetContextSnippet returns a compact context around the reference location
// numLines specifies how many lines before and after to include
func GetContextSnippet(ctx context.Context, client *lsp.Client, loc protocol.Location, numLines int) (string, error) {
	// Convert URI to filesystem path
	filePath, err := url.PathUnescape(strings.TrimPrefix(string(loc.URI), "file://"))
	if err != nil {
		return "", fmt.Errorf("failed to unescape URI: %w", err)
	}

	// Read the file
	content, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to read file: %w", err)
	}

	lines := strings.Split(string(content), "\n")

	// Calculate the range to show
	startLine := int(loc.Range.Start.Line) - numLines
	if startLine < 0 {
		startLine = 0
	}

	endLine := int(loc.Range.Start.Line) + numLines
	if endLine >= len(lines) {
		endLine = len(lines) - 1
	}

	// Get the relevant lines
	contextLines := lines[startLine : endLine+1]

	// Find the line with the reference
	refLineIdx := int(loc.Range.Start.Line) - startLine

	// Format the context
	var result strings.Builder

	// Get the line with the reference and shorten if needed
	refLine := contextLines[refLineIdx]
	refLine = strings.TrimSpace(refLine)

	// Truncate line if it's too long (keep it reasonable for display)
	const maxLineLength = 100
	if len(refLine) > maxLineLength {
		startChar := int(loc.Range.Start.Character)
		// Try to center the reference in the shortened context
		startPos := startChar - (maxLineLength / 2)
		if startPos < 0 {
			startPos = 0
		}
		endPos := startPos + maxLineLength
		if endPos > len(refLine) {
			endPos = len(refLine)
			startPos = endPos - maxLineLength
			if startPos < 0 {
				startPos = 0
			}
		}

		if startPos > 0 {
			refLine = "..." + refLine[startPos:endPos]
		} else {
			refLine = refLine[:endPos] + "..."
		}
	}

	result.WriteString(refLine)
	return result.String(), nil
}
