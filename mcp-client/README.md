In vscode you have to `code .` inside this project for python environment to load.

# Find references
python main.py find_references symbolName=ScopeInfo showLineNumbers=true
python main.py find_references symbolName=debugLogger showLineNumbers=true
python main.py find_references symbolName=server showLineNumbers=true

# Get definition
python main.py read_definition symbolName=ApplyTextEditArgs showLineNumbers=true
python main.py read_definition symbolName=server showLineNumbers=true

# Get diagnostics for a specific file
python main.py get_diagnostics filePath=internal/tools/diagnostics.go showLineNumbers=true includeContext=false

# Get hover info (assuming line/column are 1-based as per Go comments)
python main.py hover filePath=internal/tools/find-references.go line=65 column=6

# Get document symbols
python main.py document_symbols filePath=internal/tools/find-references.go

# Apply a hypothetical edit (ensure JSON structure is correct if needed)
# Note: Passing complex structures like lists of objects via key=value is hard.
# This tool might require modifications or a different input method (e.g., reading JSON from a file)
# python main.py apply_text_edit filePath=myfile.go edits='[{"range": ...}]' # <-- This simple parsing won't work well for JSON

# Call a tool with no arguments (if any exist)
# python main.py some_tool_with_no_args
