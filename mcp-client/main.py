import argparse
import asyncio
import json
import sys
from mcp import ClientSession, StdioServerParameters, types
from mcp.client.stdio import stdio_client

# --- Server Configuration (Modify as needed) ---
SERVER_COMMAND = "/Users/orsen/Develop/mcp-language-server/mcp-language-server"
SERVER_ARGS = ["--workspace", "/Users/orsen/Develop/mcp-language-server", "--lsp", "gopls"]
SERVER_ENV = {}
SERVER_NAME = "language-server"  # Used for logging/identification if needed


def parse_value(value_str):
    """Attempts to parse a string value into bool, int, float, or keeps as string."""
    val_lower = value_str.lower()
    if val_lower == 'true':
        return True
    if val_lower == 'false':
        return False
    try:
        return int(value_str)
    except ValueError:
        pass
    try:
        return float(value_str)
    except ValueError:
        pass
    # If it's quoted, remove quotes (basic handling)
    if len(value_str) >= 2 and value_str.startswith('"') and value_str.endswith('"'):
        return value_str[1:-1]
    if len(value_str) >= 2 and value_str.startswith("'") and value_str.endswith("'"):
        return value_str[1:-1]
    return value_str


def parse_tool_arguments(arg_list):
    """Parses a list of 'key=value' strings into a dictionary."""
    parsed_args = {}
    if not arg_list:
        return parsed_args, None  # No arguments provided is valid

    for arg_pair in arg_list:
        if '=' not in arg_pair:
            return None, f"Invalid argument format: '{arg_pair}'. Expected 'key=value'."
        key, value_str = arg_pair.split('=', 1)
        if not key:
            return None, f"Argument key cannot be empty in '{arg_pair}'."
        parsed_args[key] = parse_value(value_str)

    return parsed_args, None


async def run_mcp_tool_cli(tool_name, tool_arguments):
    """Connects to the MCP server and executes the specified tool."""

    print(f"--- Configuration ---")
    print(f"Server Command: {SERVER_COMMAND}")
    print(f"Server Args: {SERVER_ARGS}")
    print(f"Target Tool: {tool_name}")
    print(f"Tool Arguments: {json.dumps(tool_arguments, indent=2)}")
    print("-" * 20 + "\n")

    server_params = StdioServerParameters(
        command=SERVER_COMMAND,
        args=SERVER_ARGS,
        env=SERVER_ENV,
    )

    try:
        print("Attempting to start server via stdio...")
        async with stdio_client(server_params) as (read_stream, write_stream):
            print("Server process likely started. Establishing MCP session...")

            async with ClientSession(read_stream, write_stream) as session:
                print("Initializing MCP session...")
                init_result = await session.initialize()
                print(f"Session initialized successfully!")
                # Optional: print capabilities if needed
                # print(f"Server capabilities: {init_result.capabilities}")

                print(f"\nCalling tool '{tool_name}'...")

                result = await session.call_tool(tool_name, arguments=tool_arguments)

                print("\n--- Tool Result ---")
                # The result type depends on what the tool returns.
                # It could be a primitive, dict, list, etc.
                if isinstance(result, (dict, list)):
                    print(json.dumps(result, indent=2))
                else:
                    print(result)
                print("-------------------\n")

                # --- New Pretty Printing Logic ---
                print("\n--- Tool Result (Formatted) ---")
                if hasattr(result, 'isError') and result.isError:
                    print("Tool call resulted in an error.")
                    if hasattr(result, 'content') and result.content:
                        error_text = ""
                        for content_item in result.content:
                            if (
                                hasattr(content_item, 'type')
                                and content_item.type == 'text'
                                and hasattr(content_item, 'text')
                            ):
                                error_text += content_item.text
                            elif isinstance(content_item, str):
                                error_text += content_item
                        if error_text:
                            print("Error details:")
                            print(error_text)
                        else:
                            print(f"Raw error result object: {result}")
                    else:
                        print(f"Raw error result object: {result}")

                elif hasattr(result, 'content') and result.content:
                    full_text_output = ""
                    for content_item in result.content:
                        if (
                            hasattr(content_item, 'type')
                            and content_item.type == 'text'
                            and hasattr(content_item, 'text')
                        ):
                            full_text_output += content_item.text
                        else:
                            full_text_output += f"\n[Unsupported Content Type: {type(content_item)}]\n{content_item}\n"
                    print(full_text_output.strip())

                else:
                    print("Tool returned a result without standard content structure:")
                    if isinstance(result, (dict, list)):
                        print(json.dumps(result, indent=2))
                    else:
                        print(result)

                print("-------------------\n")
                print("Client finished.")

    except Exception as e:
        print(f"\n--- An Error Occurred ---", file=sys.stderr)
        print(f"Error type: {type(e).__name__}", file=sys.stderr)
        print(f"Error details: {e}", file=sys.stderr)
        import traceback

        traceback.print_exc(file=sys.stderr)
        print("-------------------------\n", file=sys.stderr)
        sys.exit(1)  # Exit with error code


def main():
    parser = argparse.ArgumentParser(
        description="MCP Client CLI to interact with a language server.",
        epilog="Example: python %(prog)s find_references symbolName=MyFunction showLineNumbers=true",
    )

    parser.add_argument(
        "tool_name",
        help="The name of the MCP tool to call (e.g., 'find_references', 'read_definition').",
    )
    parser.add_argument(
        "tool_args",
        nargs='*',  # 0 or more arguments
        help="Arguments for the tool, specified as 'key=value' pairs. "
        "Values 'true'/'false' are parsed as booleans, numbers as int/float if possible, otherwise as strings. "
        "Use quotes for values with spaces if your shell requires it (e.g., 'filePath=\"my file.go\"').",
    )

    args = parser.parse_args()

    # Parse the key=value arguments into a dictionary
    arguments_dict, error_msg = parse_tool_arguments(args.tool_args)

    if error_msg:
        parser.error(error_msg)  # argparse handles printing usage and exiting

    # Run the async main function
    try:
        asyncio.run(run_mcp_tool_cli(args.tool_name, arguments_dict))
    except KeyboardInterrupt:
        print("\nClient interrupted by user.")
        sys.exit(0)


if __name__ == "__main__":
    main()
