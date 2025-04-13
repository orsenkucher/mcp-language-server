package tools

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/isaacphi/mcp-language-server/internal/lsp"
	"github.com/isaacphi/mcp-language-server/internal/protocol"
)

func ReadDefinition(ctx context.Context, client *lsp.Client, symbolName string, showLineNumbers bool) (string, error) {
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

	var definitions []string
	for _, symbol := range results {
		kind := ""
		container := ""

		// Skip symbols that we are not looking for. workspace/symbol may return
		// a large number of fuzzy matches.
		switch v := symbol.(type) {
		case *protocol.SymbolInformation:
			// SymbolInformation results have richer data.
			kind = protocol.TableKindMap[v.Kind]
			if v.ContainerName != "" {
				container = v.ContainerName
			}
			if v.Kind == protocol.Method && strings.HasSuffix(symbol.GetName(), symbolName) {
				break
			}
			if symbol.GetName() != symbolName {
				continue
			}
		default:
			if symbol.GetName() != symbolName {
				continue
			}
		}

		loc := symbol.GetLocation()
		filePath := strings.TrimPrefix(string(loc.URI), "file://")

		definition, loc, err := GetFullDefinition(ctx, client, loc)
		if err != nil {
			log.Printf("Error getting definition: %v\n", err)
			continue
		}

		// Create a cleaner header with key information
		header := fmt.Sprintf("Symbol: %s\nFile: %s\n",
			symbol.GetName(),
			filePath)

		// Add kind and container if available
		if kind != "" {
			header += fmt.Sprintf("Kind: %s\n", kind)
		}
		if container != "" {
			header += fmt.Sprintf("Container Name: %s\n", container)
		}

		// Add location information but simplified
		header += fmt.Sprintf("Location: Lines %d-%d\n",
			loc.Range.Start.Line+1,
			loc.Range.End.Line+1)

		// Format the code with line numbers if requested
		if showLineNumbers {
			definition = addLineNumbers(definition, int(loc.Range.Start.Line)+1)
		}

		definitions = append(definitions, header+definition)
	}

	if len(definitions) == 0 {
		return fmt.Sprintf("%s not found", symbolName), nil
	}

	return strings.Join(definitions, "\n\n"), nil
}
