import asyncio  # Still potentially useful for type hints, maybe some low-level details
import json
import os
import sys
import anyio  # Use anyio for process management and streams
import traceback  # Import traceback for detailed error printing

from mcp import ClientSession
import mcp.types as types

# --- Configuration (remains the same) ---
server_name = "language-server"
server_command = "/Users/orsen/Develop/mcp-language-server/mcp-language-server"
server_args = [
    "--workspace",
    "/Users/orsen/Develop/mcp-language-server",
    "--lsp",
    "gopls",
]
# Use anyio.Process's 'env' parameter directly
# server_env = os.environ.copy() # No longer needed here
tool_name = "find_references"
tool_arguments = {
    "symbolName": "ScopeInfo",
    "showLineNumbers": True,
}


# --- Function to read and print stderr lines concurrently (adapted for anyio stream) ---
async def read_stderr_stream_anyio(stderr_stream: anyio.abc.ReceiveStream[bytes]):
    """Reads byte chunks from the server's stderr anyio stream and prints them as lines."""
    print("[CLIENT] Stderr reader task started.", file=sys.stderr)
    buffer = b""
    try:
        async for chunk in stderr_stream:
            buffer += chunk
            while True:
                # Find the position of the first newline
                try:
                    newline_pos = buffer.index(b'\n')
                except ValueError:
                    # No newline found in the current buffer
                    break

                # Extract the line (including the newline)
                line_bytes = buffer[: newline_pos + 1]
                # Update the buffer
                buffer = buffer[newline_pos + 1 :]

                # Decode and print the line
                try:
                    line_str = line_bytes.decode('utf-8').rstrip()
                    print(f"[SERVER STDERR] {line_str}", file=sys.stderr)
                except UnicodeDecodeError:
                    print(
                        f"[SERVER STDERR] <failed to decode line: {line_bytes!r}>", file=sys.stderr
                    )

        # Process any remaining data in the buffer after the stream closes
        if buffer:
            try:
                line_str = buffer.decode('utf-8').rstrip()
                print(f"[SERVER STDERR] {line_str}", file=sys.stderr)
            except UnicodeDecodeError:
                print(f"[SERVER STDERR] <failed to decode final data: {buffer!r}>", file=sys.stderr)

    except anyio.EndOfStream:
        print("[CLIENT] Stderr stream reached EOF.", file=sys.stderr)
    except Exception as e:
        # Catch specific exceptions like ClosedResourceError if needed
        if isinstance(e, (anyio.ClosedResourceError, anyio.BrokenResourceError)):
            print(f"[CLIENT] Stderr stream closed/broken: {type(e).__name__}", file=sys.stderr)
        else:
            print(f"[CLIENT] Error reading stderr: {e}", file=sys.stderr)
            traceback.print_exc(file=sys.stderr)  # Print traceback for unexpected errors
    finally:
        print("[CLIENT] Stderr reader task finished.", file=sys.stderr)


# --- Main client function (modified for anyio.open_process) ---
async def run_language_server_client_with_stderr():
    process: anyio.abc.Process | None = None
    stderr_task: anyio.abc.TaskStatus | None = None  # Using TaskGroup for better structure

    print(f"Configuring client for server: {server_name}")
    print(f"Command: {server_command}")
    print(f"Args: {server_args}")

    try:
        # Use anyio.open_process
        print("[CLIENT] Starting server process using anyio.open_process...", file=sys.stderr)
        process = await anyio.open_process(
            [server_command] + server_args,  # Command must be a list/tuple
            stdin=anyio.subprocess.PIPE,
            stdout=anyio.subprocess.PIPE,
            stderr=anyio.subprocess.PIPE,
            # Pass environment variables if needed, defaults to parent env
            # env=os.environ.copy()
        )
        print(f"[CLIENT] Server process started (PID: {process.pid}).", file=sys.stderr)

        # Get the anyio streams directly
        if process.stdin is None or process.stdout is None or process.stderr is None:
            raise RuntimeError("Failed to get process streams from anyio.open_process")

        anyio_write_stream = process.stdin
        anyio_read_stream = process.stdout
        anyio_stderr_stream = process.stderr

        # Use a TaskGroup to manage the stderr reader and the main session
        async with anyio.create_task_group() as tg:
            print("[CLIENT] Starting stderr reader task within TaskGroup...", file=sys.stderr)
            stderr_task = await tg.start(
                read_stderr_stream_anyio, anyio_stderr_stream
            )  # Start task

            print("[CLIENT] Establishing MCP session...", file=sys.stderr)
            # MCP ClientSession should work directly with these anyio streams
            async with ClientSession(anyio_read_stream, anyio_write_stream) as session:
                print("[CLIENT] Initializing MCP session...", file=sys.stderr)
                init_result = await session.initialize()
                print(f"[CLIENT] Session initialized successfully!", file=sys.stderr)

                print(f"\n[CLIENT] Calling tool '{tool_name}' on server '{server_name}'...")
                print(f"[CLIENT] Arguments: {json.dumps(tool_arguments, indent=2)}")

                result = await session.call_tool(tool_name, arguments=tool_arguments)

                # --- Pretty Print Result (remains the same) ---
                print("\n--- Tool Result ---")
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
                # --- End Pretty Print ---

                print("[CLIENT] Client finished main logic.")
            # Exiting ClientSession context manager should close stdin/stdout streams

            # Optionally wait for stderr task to finish if needed, but TaskGroup manages it
            print(
                "[CLIENT] Main session finished, TaskGroup will wait for stderr reader.",
                file=sys.stderr,
            )

        # TaskGroup ensures both session and stderr reader are finished/cancelled here
        print("[CLIENT] TaskGroup finished.", file=sys.stderr)

    except Exception as e:
        # Handle exceptions, including ExceptionGroup from TaskGroup
        print(f"\n--- [CLIENT] An Error Occurred ---", file=sys.stderr)
        if isinstance(e, BaseExceptionGroup):
            print(
                f"Error Type: ExceptionGroup ({len(e.exceptions)} sub-exceptions)", file=sys.stderr
            )
            for i, sub_exc in enumerate(e.exceptions):
                print(f"\n--- Sub-exception {i+1}/{len(e.exceptions)} ---", file=sys.stderr)
                print(f"Type: {type(sub_exc).__name__}", file=sys.stderr)
                print(f"Details: {sub_exc}", file=sys.stderr)
                traceback.print_exception(sub_exc, file=sys.stderr)
                print("------------------------", file=sys.stderr)
        else:
            print(f"Error type: {type(e).__name__}", file=sys.stderr)
            print(f"Error details: {e}", file=sys.stderr)
            traceback.print_exc(file=sys.stderr)
        print("-----------------------------------\n", file=sys.stderr)

    finally:
        print("[CLIENT] Entering cleanup phase...", file=sys.stderr)
        # Cleanup process using anyio methods
        if process:
            if process.returncode is None:
                print(
                    f"[CLIENT] Terminating server process (PID: {process.pid})...", file=sys.stderr
                )
                try:
                    process.terminate()
                    # Wait for termination with a timeout
                    async with anyio.fail_after(5):
                        await process.wait()
                    print(
                        f"[CLIENT] Server process terminated (Return Code: {process.returncode}).",
                        file=sys.stderr,
                    )
                except TimeoutError:  # anyio raises TimeoutError on fail_after
                    print(
                        f"[CLIENT] Server process did not terminate gracefully, killing (PID: {process.pid})...",
                        file=sys.stderr,
                    )
                    process.kill()
                    await process.wait()  # Wait after killing
                    print(
                        f"[CLIENT] Server process killed (Return Code: {process.returncode}).",
                        file=sys.stderr,
                    )
                except Exception as e_term:
                    print(
                        f"[CLIENT] Error terminating/waiting for process: {e_term}", file=sys.stderr
                    )
            else:
                print(
                    f"[CLIENT] Server process already exited (Return Code: {process.returncode}).",
                    file=sys.stderr,
                )

            # Explicitly close the process resource (good practice with anyio)
            print("[CLIENT] Closing process resource...", file=sys.stderr)
            await process.aclose()
            print("[CLIENT] Process resource closed.", file=sys.stderr)

        print("[CLIENT] Cleanup finished.", file=sys.stderr)


if __name__ == "__main__":
    # Ensure traceback is imported
    import traceback

    try:
        # Run using anyio
        anyio.run(run_language_server_client_with_stderr)
    except KeyboardInterrupt:
        print("\n[CLIENT] Script interrupted by user.", file=sys.stderr)
