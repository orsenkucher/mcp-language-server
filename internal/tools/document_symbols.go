package tools

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/isaacphi/mcp-language-server/internal/lsp"
	"github.com/isaacphi/mcp-language-server/internal/protocol"
)

// GetDocumentSymbols retrieves all symbols in a document and formats them in a hierarchical structure
func GetDocumentSymbols(ctx context.Context, client *lsp.Client, filePath string, showLineNumbers bool) (string, error) {
	// Open the file if not already open
	err := client.OpenFile(ctx, filePath)
	if err != nil {
		return "", fmt.Errorf("could not open file: %v", err)
	}

	// Convert to URI format for LSP protocol
	uri := protocol.DocumentUri("file://" + filePath)

	// Create the document symbol parameters
	symParams := protocol.DocumentSymbolParams{
		TextDocument: protocol.TextDocumentIdentifier{
			URI: uri,
		},
	}

	// Execute the document symbol request
	symResult, err := client.DocumentSymbol(ctx, symParams)
	if err != nil {
		return "", fmt.Errorf("failed to get document symbols: %v", err)
	}

	symbols, err := symResult.Results()
	if err != nil {
		return "", fmt.Errorf("failed to process document symbols: %v", err)
	}

	if len(symbols) == 0 {
		return fmt.Sprintf("No symbols found in %s", filePath), nil
	}

	var result strings.Builder
	result.WriteString(fmt.Sprintf("Symbols in %s\n\n", filePath))

	// Format symbols hierarchically
	formatSymbols(&result, symbols, 0, showLineNumbers)

	return result.String(), nil
}

// formatSymbols recursively formats symbols with proper indentation
func formatSymbols(sb *strings.Builder, symbols []protocol.DocumentSymbolResult, level int, showLineNumbers bool) {
	indent := strings.Repeat("  ", level)

	for _, sym := range symbols {
		// Get symbol information
		name := sym.GetName()

		// Default to Symbol
		kindStr := "[Symbol]"

		// Format location information
		location := ""
		if showLineNumbers {
			r := sym.GetRange()
			if r.Start.Line == r.End.Line {
				location = fmt.Sprintf("Line %d", r.Start.Line+1)
			} else {
				location = fmt.Sprintf("Lines %d-%d", r.Start.Line+1, r.End.Line+1)
			}
		}

		// Try to extract kind through reflection since we have different struct types
		// with different ways to access Kind
		symValue := reflect.ValueOf(sym).Elem()

		// Try direct Kind field
		if kindField := symValue.FieldByName("Kind"); kindField.IsValid() {
			kind := protocol.SymbolKind(kindField.Uint())
			kindStr = getSymbolKindString(kind)
		} else {
			// Try BaseSymbolInformation.Kind
			if baseField := symValue.FieldByName("BaseSymbolInformation"); baseField.IsValid() {
				if kindField := baseField.FieldByName("Kind"); kindField.IsValid() {
					kind := protocol.SymbolKind(kindField.Uint())
					kindStr = getSymbolKindString(kind)
				}
			}
		}

		// Format the symbol entry
		if location != "" {
			sb.WriteString(fmt.Sprintf("%s%s %s (%s)\n", indent, kindStr, name, location))
		} else {
			sb.WriteString(fmt.Sprintf("%s%s %s\n", indent, kindStr, name))
		}

		// Format children if it's a DocumentSymbol
		if ds, ok := sym.(*protocol.DocumentSymbol); ok && len(ds.Children) > 0 {
			childSymbols := make([]protocol.DocumentSymbolResult, len(ds.Children))
			for i := range ds.Children {
				childSymbols[i] = &ds.Children[i]
			}
			formatSymbols(sb, childSymbols, level+1, showLineNumbers)
		}
	}
}

// getSymbolKindString converts SymbolKind to a descriptive string
func getSymbolKindString(kind protocol.SymbolKind) string {
	switch kind {
	case 1: // File
		return "[File]"
	case 2: // Module
		return "[Module]"
	case 3: // Namespace
		return "[Namespace]"
	case 4: // Package
		return "[Package]"
	case 5: // Class
		return "[Class]"
	case 6: // Method
		return "[Method]"
	case 7: // Property
		return "[Property]"
	case 8: // Field
		return "[Field]"
	case 9: // Constructor
		return "[Constructor]"
	case 10: // Enum
		return "[Enum]"
	case 11: // Interface
		return "[Interface]"
	case 12: // Function
		return "[Function]"
	case 13: // Variable
		return "[Variable]"
	case 14: // Constant
		return "[Constant]"
	case 15: // String
		return "[String]"
	case 16: // Number
		return "[Number]"
	case 17: // Boolean
		return "[Boolean]"
	case 18: // Array
		return "[Array]"
	case 19: // Object
		return "[Object]"
	case 20: // Key
		return "[Key]"
	case 21: // Null
		return "[Null]"
	case 22: // EnumMember
		return "[EnumMember]"
	case 23: // Struct
		return "[Struct]"
	case 24: // Event
		return "[Event]"
	case 25: // Operator
		return "[Operator]"
	case 26: // TypeParameter
		return "[TypeParameter]"
	default:
		return "[Unknown]"
	}
}
