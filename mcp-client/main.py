import asyncio
import json
from mcp import ClientSession, StdioServerParameters
from mcp.client.stdio import stdio_client
import mcp.types as types  # Good practice for potential type hints or checking


async def run_language_server_client():
    # --- Configuration from your JSON ---
    server_name = "language-server"  # Used for potential logging/identification
    server_command = "/Users/orsen/Develop/mcp-language-server/mcp-language-server"
    server_args = ["--workspace", "/Users/orsen/Develop/mcp-language-server", "--lsp", "gopls"]
    server_env = {}  # Empty as per your config

    # --- Request Details ---
    # The MCP SDK's call_tool likely takes the base tool name.
    # The SDK should handle constructing the full JSON-RPC method
    # like "mcp__language-server__find_references" internally based on the context.
    # If this doesn't work, you might need to investigate if a lower-level
    # request mechanism is needed or if the tool name needs the prefix.
    tool_name = "find_references"
    tool_arguments = {"symbolName": "ScopeInfo", "showLineNumbers": True}
    # The full JSON-RPC method name as per your example request:
    # full_method_name = f"mcp__{server_name}__{tool_name}"

    # --- Client Logic ---
    print(f"Configuring client for server: {server_name}")
    print(f"Command: {server_command}")
    print(f"Args: {server_args}")

    # Create server parameters for stdio connection
    server_params = StdioServerParameters(
        command=server_command,
        args=server_args,
        env=server_env,
    )

    try:
        # Launch the server process and get communication streams
        print("Attempting to start server via stdio...")
        async with stdio_client(server_params) as (read_stream, write_stream):
            print("Server process likely started. Establishing MCP session...")

            # Create and manage the client session
            async with ClientSession(read_stream, write_stream) as session:
                print("Initializing MCP session...")
                # Initialize the connection (sends initialize request, receives response)
                init_result = await session.initialize()
                print(f"Session initialized successfully!")
                # You can optionally inspect init_result.capabilities here
                # print(f"Server capabilities: {init_result.capabilities}")

                print(f"\nCalling tool '{tool_name}' on server '{server_name}'...")
                print(f"Arguments: {json.dumps(tool_arguments, indent=2)}")

                # Call the specific tool using the base name
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
                print("\n--- Tool Result ---")
                if hasattr(result, 'isError') and result.isError:
                    print("Tool call resulted in an error.")
                    # Try to print error content if available
                    if hasattr(result, 'content') and result.content:
                        error_text = ""
                        for content_item in result.content:
                            if (
                                hasattr(content_item, 'type')
                                and content_item.type == 'text'
                                and hasattr(content_item, 'text')
                            ):
                                error_text += content_item.text
                            elif isinstance(content_item, str):  # Simple string error?
                                error_text += content_item
                        if error_text:
                            print("Error details:")
                            print(error_text)
                        else:  # Fallback if content isn't helpful text
                            print(f"Raw error result object: {result}")
                    else:
                        # No specific content, print the raw result
                        print(f"Raw error result object: {result}")

                elif hasattr(result, 'content') and result.content:
                    # Process successful result with content
                    full_text_output = ""
                    for content_item in result.content:
                        # Check if it's a TextContent object and extract its text
                        # You might need to import types: from mcp import types
                        # if isinstance(content_item, types.TextContent):
                        # Or more generically check attributes:
                        if (
                            hasattr(content_item, 'type')
                            and content_item.type == 'text'
                            and hasattr(content_item, 'text')
                        ):
                            full_text_output += content_item.text
                        # Add elif clauses here if the tool might return other content types
                        # like ImageContent, etc., and you want to handle them.
                        else:
                            # Append the representation of unknown content types
                            full_text_output += f"\n[Unsupported Content Type: {type(content_item)}]\n{content_item}\n"

                    # Print the concatenated text content
                    print(
                        full_text_output.strip()
                    )  # Use strip() to remove leading/trailing whitespace

                else:
                    # Handle cases where the result might be simple (None, bool, number)
                    # or an unexpected object structure without 'content'.
                    print("Tool returned a result without standard content structure:")
                    if isinstance(result, (dict, list)):
                        print(json.dumps(result, indent=2))
                    else:
                        print(result)  # Print the raw result object/value

                print("-------------------\n")

                print("Client finished.")

    except Exception as e:
        print(f"\n--- An Error Occurred ---")
        print(f"Error type: {type(e).__name__}")
        print(f"Error details: {e}")
        import traceback

        traceback.print_exc()
        print("-------------------------\n")


if __name__ == "__main__":
    # Ensure the script is run within an asyncio event loop
    try:
        asyncio.run(run_language_server_client())
    except KeyboardInterrupt:
        print("\nClient interrupted.")
