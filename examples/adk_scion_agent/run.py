"""Custom ADK runner entrypoint with --input flag support.

The stock `adk run` CLI does not yet support --input in the released PyPI
package. This module provides a lightweight replacement that:

  1. Accepts --input <message> to deliver an initial task to the agent.
  2. Loads the agent from this package.
  3. Uses ADK's InMemoryRunner to execute it.
  4. Falls through to an interactive input() loop so that scion can deliver
     follow-up messages via tmux send-keys.

Usage (standalone):
    python -m adk_scion_agent.run --input "write a hello world script"

Usage (container CMD):
    python -m adk_scion_agent.run
"""

import argparse
import asyncio

from google.adk.runners import InMemoryRunner
from google.genai import types

from .agent import root_agent


APP_NAME = "adk_scion_agent"
USER_ID = "scion_user"


async def _run(initial_message: str | None) -> None:
    runner = InMemoryRunner(agent=root_agent, app_name=APP_NAME)

    session = await runner.session_service.create_session(
        app_name=APP_NAME, user_id=USER_ID
    )

    async def send(text: str) -> None:
        content = types.Content(role="user", parts=[types.Part(text=text)])
        async for event in runner.run_async(
            user_id=USER_ID, session_id=session.id, new_message=content
        ):
            if event.content and event.content.parts:
                text_out = "".join(p.text or "" for p in event.content.parts)
                if text_out:
                    print(f"[{event.author}]: {text_out}", flush=True)

    # Process initial --input message if provided.
    if initial_message:
        print(f"[user]: {initial_message}", flush=True)
        await send(initial_message)

    # Interactive loop — scion delivers follow-up input via tmux send-keys.
    while True:
        try:
            query = input("[user]: ")
        except (EOFError, KeyboardInterrupt):
            break
        if not query or not query.strip():
            continue
        if query.strip() == "exit":
            break
        await send(query)

    await runner.close()


def main() -> None:
    parser = argparse.ArgumentParser(
        description="Run the ADK scion agent with optional initial input."
    )
    parser.add_argument(
        "--input",
        dest="initial_message",
        default=None,
        help="Initial message to send to the agent before entering interactive mode.",
    )
    args = parser.parse_args()
    asyncio.run(_run(args.initial_message))


if __name__ == "__main__":
    main()
