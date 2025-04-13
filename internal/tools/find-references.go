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
	"github.com/isaacphi/mcp-language-server/internal/utilities"
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

// ScopeInfo stores information about a code scope including its name and kind
type ScopeInfo struct {
	Name    string              // Name of the scope
	Kind    protocol.SymbolKind // Kind of the symbol (if available, 0 otherwise)
	HasKind bool                // Whether we have kind information
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
			fileInfo := fmt.Sprintf("File: %s (%d references)", filePath, len(fileRefs))
			allReferences = append(allReferences, fileInfo)

			// Group references by scope within each file
			// We'll use ScopeIdentifier to uniquely identify each scope
			scopeRefs := make(map[ScopeIdentifier][]ReferencePosition)
			scopeTexts := make(map[ScopeIdentifier]string)
			scopeInfos := make(map[ScopeIdentifier]ScopeInfo)

			// Try to get document symbols for the file once
			var docSymbols []protocol.DocumentSymbolResult
			symParams := protocol.DocumentSymbolParams{
				TextDocument: protocol.TextDocumentIdentifier{
					URI: uri,
				},
			}

			symResult, symErr := client.DocumentSymbol(ctx, symParams)
			if symErr == nil {
				docSymbols, _ = symResult.Results()
			}

			// First pass: get scope for each reference
			for _, ref := range fileRefs {
				// Some references might be in imports or attributes which might not be
				// part of a formal scope recognized by the language server

				// Try standard scope finding first
				fullScope, scopeLoc, err := GetFullDefinition(ctx, client, ref)
				if err != nil {
					// If we can't find a scope, it might be in an import or attribute
					// Get a smaller context around the reference instead
					contextLines := 10 // Get 10 lines of context
					smallerScope, err := GetDefinitionWithContext(ctx, client, ref, contextLines)
					if err != nil {
						continue
					}

					// Create a smaller scope range
					startLine := ref.Range.Start.Line
					if startLine > uint32(contextLines) {
						startLine -= uint32(contextLines)
					} else {
						startLine = 0
					}

					endLine := ref.Range.Start.Line + uint32(contextLines)

					// Update the scopeLoc
					scopeLoc = protocol.Location{
						URI: ref.URI,
						Range: protocol.Range{
							Start: protocol.Position{Line: startLine},
							End:   protocol.Position{Line: endLine},
						},
					}

					fullScope = smallerScope
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
				if _, exists := scopeInfos[scopeID]; !exists {
					// Check if this might be a reference in an attribute or import
					isAttribute := false
					scopeLines := strings.Split(fullScope, "\n")
					refLineIdx := int(ref.Range.Start.Line - scopeID.StartLine)

					if refLineIdx >= 0 && refLineIdx < len(scopeLines) {
						refLine := strings.TrimSpace(scopeLines[refLineIdx])
						if strings.HasPrefix(refLine, "#[") ||
							strings.HasPrefix(refLine, "use ") ||
							strings.HasPrefix(refLine, "import ") {
							isAttribute = true
						}
					}

					var scopeName string

					if isAttribute {
						// For attributes/imports, use the line containing the reference as the scope name
						if refLineIdx >= 0 && refLineIdx < len(scopeLines) {
							scopeName = "Attribute/Import: " + strings.TrimSpace(scopeLines[refLineIdx])
						} else {
							scopeName = "Attribute/Import"
						}
					} else {
						// Try regular scope name detection

						// First attempt: Try to use the document symbols to get an accurate scope name
						if len(docSymbols) > 0 {
							// Find a symbol that contains our reference position
							var findSymbolInRange func([]protocol.DocumentSymbolResult, protocol.Range) string
							findSymbolInRange = func(symbols []protocol.DocumentSymbolResult, targetRange protocol.Range) string {
								for _, sym := range symbols {
									symRange := sym.GetRange()

									// Check if this symbol contains our scope
									if symRange.Start.Line <= targetRange.Start.Line &&
										symRange.End.Line >= targetRange.End.Line {

										// Check if it has children that might be a better match
										if ds, ok := sym.(*protocol.DocumentSymbol); ok && len(ds.Children) > 0 {
											childSymbols := make([]protocol.DocumentSymbolResult, len(ds.Children))
											for i := range ds.Children {
												childSymbols[i] = &ds.Children[i]
											}

											if childName := findSymbolInRange(childSymbols, targetRange); childName != "" {
												return childName
											}

											// This is the best match, get its name with kind
											kindStr := utilities.ExtractSymbolKind(ds)
											if kindStr != "" {
												return fmt.Sprintf("%s %s", kindStr, ds.Name)
											}
											return ds.Name
										}

										return sym.GetName()
									}
								}
								return ""
							}

							// Try to find a symbol containing our scope range
							if scopeName = findSymbolInRange(docSymbols, scopeLoc.Range); scopeName != "" {
								// Use the symbol name from LSP
							} else {
								// Fallback: Parse the scope text to find a good name

								// Extract the function/method signature - the first line of actual code
								// Look specifically for definition patterns across languages
								foundDefinition := false
								functionPatterns := []string{
									"func ", "fn ", "def ", "pub fn", "async fn",
								}
								typePatterns := []string{
									"type ", "class ", "struct ", "enum ", "interface ",
									"pub struct", "pub enum", "pub trait",
								}

								// First pass: Look for function/method definitions
								for _, line := range scopeLines {
									trimmed := strings.TrimSpace(line)
									if trimmed == "" {
										continue
									}

									// Skip comments and attributes
									if strings.HasPrefix(trimmed, "///") ||
										strings.HasPrefix(trimmed, "//") ||
										strings.HasPrefix(trimmed, "/*") ||
										strings.HasPrefix(trimmed, "*") ||
										strings.HasPrefix(trimmed, "*/") ||
										strings.HasPrefix(trimmed, "#[") {
										continue
									}

									// Check for function patterns
									for _, pattern := range functionPatterns {
										if strings.Contains(trimmed, pattern) {
											// Found a function signature - take the full line
											scopeName = trimmed
											foundDefinition = true
											break
										}
									}

									if foundDefinition {
										break
									}

									// Check for type patterns
									for _, pattern := range typePatterns {
										if strings.Contains(trimmed, pattern) {
											// Found a type definition - take the full line
											scopeName = trimmed
											foundDefinition = true
											break
										}
									}

									if foundDefinition {
										break
									}

									// If no function or type pattern matched but this is a non-comment line
									// Use it as our scope name
									scopeName = trimmed
									break
								}

								// If we couldn't find anything, use the first non-empty line
								if scopeName == "" && len(scopeLines) > 0 {
									for _, line := range scopeLines {
										trimmed := strings.TrimSpace(line)
										if trimmed != "" {
											scopeName = trimmed
											break
										}
									}
								}
							}
						}
					}

					// Don't truncate the scope name - show full signature
					scopeInfo := ScopeInfo{
						Name:    scopeName,
						Kind:    0, // Default to unknown kind
						HasKind: false,
					}

					// If we found this name via document symbols, try to get the kind too
					if len(docSymbols) > 0 {
						// Find a symbol that contains this scope range
						for _, sym := range docSymbols {
							symRange := sym.GetRange()

							// Check if this symbol contains our scope
							if symRange.Start.Line <= scopeID.StartLine &&
								symRange.End.Line >= scopeID.EndLine {

								// Try to get the kind via reflection
								if ds, ok := sym.(*protocol.DocumentSymbol); ok {
									scopeInfo.Kind = ds.Kind
									scopeInfo.HasKind = true
									break
								}
							}
						}
					}

					scopeInfos[scopeID] = scopeInfo
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

				// Get scope information for this scope
				scopeInfo := scopeInfos[scopeID]

				// Add debug information about the scope kind being processed
				debugInfo := fmt.Sprintf("DEBUG: Scope=%s, HasKind=%v, Kind=%d",
					scopeInfo.Name, scopeInfo.HasKind, scopeInfo.Kind)
				allReferences = append(allReferences, debugInfo)

				// Format the scope header with kind information if available
				var scopeHeader string
				if scopeInfo.HasKind {
					// Use the language server's kind information for the symbol
					kindStr := utilities.GetSymbolKindString(scopeInfo.Kind)
					scopeHeader = fmt.Sprintf("  %s %s (lines %d-%d, %d references)",
						kindStr,
						scopeInfo.Name,
						scopeID.StartLine+1,
						scopeID.EndLine+1,
						len(positions))
				} else {
					// Fallback to simple scope name
					scopeHeader = fmt.Sprintf("  Scope: %s (lines %d-%d, %d references)",
						scopeInfo.Name,
						scopeID.StartLine+1,
						scopeID.EndLine+1,
						len(positions))
				}
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

				// Get the scope content
				scopeText := scopeTexts[scopeID]
				scopeLines := strings.Split(scopeText, "\n")

				// For very large scopes, show only relevant parts
				if len(scopeLines) > 50 { // Only truncate if scope is larger than 50 lines
					// Create a map of important lines to always include
					importantLines := make(map[int]bool)

					// Always include the first 5 lines (for context/signature)
					for i := 0; i < 5 && i < len(scopeLines); i++ {
						importantLines[i] = true
					}

					// Always include the last 3 lines (for closing braces)
					for i := len(scopeLines) - 3; i < len(scopeLines) && i >= 0; i++ {
						importantLines[i] = true
					}

					// Always include reference lines and 2 lines of context above and below
					for _, hlLine := range highlightLines {
						for offset := -2; offset <= 2; offset++ {
							lineIdx := hlLine + offset
							if lineIdx >= 0 && lineIdx < len(scopeLines) {
								importantLines[lineIdx] = true
							}
						}
					}

					// Build the truncated output with proper line references
					var truncatedLines []string
					inSkipSection := false
					lastShownIndex := -1

					for i := 0; i < len(scopeLines); i++ {
						if importantLines[i] {
							// If we were in a skip section, add a marker with line count
							if inSkipSection {
								skippedLines := i - lastShownIndex - 1
								if skippedLines > 0 {
									truncatedLines = append(truncatedLines, fmt.Sprintf("    ... %d lines skipped ...", skippedLines))
								}
								inSkipSection = false
							}
							truncatedLines = append(truncatedLines, scopeLines[i])
							lastShownIndex = i
						} else if !inSkipSection && lastShownIndex >= 0 {
							inSkipSection = true
						}
					}

					// If we ended in a skip section, add a final marker
					if inSkipSection && lastShownIndex < len(scopeLines)-1 {
						skippedLines := len(scopeLines) - lastShownIndex - 1
						if skippedLines > 0 {
							truncatedLines = append(truncatedLines, fmt.Sprintf("    ... %d lines skipped ...", skippedLines))
						}
					}

					// Replace the scope lines with our truncated version
					scopeLines = truncatedLines
				}

				// Add line numbers if requested
				var formattedScope string
				if showLineNumbers {
					var builder strings.Builder
					lineNum := int(scopeID.StartLine) + 1

					for i, line := range scopeLines {
						// Check if this is a skipped lines marker
						if strings.Contains(line, "lines skipped") {
							// Extract the number of lines skipped
							var skipped int
							_, err := fmt.Sscanf(line, "    ... %d lines skipped ...", &skipped)
							if err != nil {
								// If we can't parse the number, assume a default
								skipped = 1
							}
							builder.WriteString(line + "\n")
							lineNum += skipped
							continue
						}

						// Determine if this is a reference line
						isRef := false
						for _, hl := range highlightLines {
							if i == hl || (lineNum == int(scopeID.StartLine)+hl+1) {
								isRef = true
								break
							}
						}

						// Add padding to line number
						numStr := fmt.Sprintf("%d", lineNum)
						padding := strings.Repeat(" ", 5-len(numStr))

						// Mark reference lines with '>' and others with '|'
						if isRef {
							builder.WriteString(fmt.Sprintf("%s%s> %s\n", padding, numStr, line))
						} else {
							builder.WriteString(fmt.Sprintf("%s%s| %s\n", padding, numStr, line))
						}
						lineNum++
					}
					formattedScope = builder.String()
				} else {
					formattedScope = strings.Join(scopeLines, "\n")
				}

				allReferences = append(allReferences, "    "+strings.ReplaceAll(formattedScope, "\n", "\n    "))
			}
		}
	}

	if len(allReferences) == 0 {
		return fmt.Sprintf("No references found for symbol: %s", symbolName), nil
	}

	return strings.Join(allReferences, "\n"), nil
}

// Helper functions for GetContextSnippet - moved to utilities package
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
