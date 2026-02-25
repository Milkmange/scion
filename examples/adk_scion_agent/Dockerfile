# ADK Scion Agent — Container Image
#
# Builds on scion-base which provides sciontool (PID 1 init), the scion user,
# tmux, git, and Python 3. This layer adds the google-adk package and the
# agent source code.
#
# Build:
#   docker build -t scion-adk-agent .
#
# The ENTRYPOINT is inherited from scion-base:
#   sciontool init -- <CMD>
#
# At runtime, scion wraps CMD in a tmux session so that `scion message` can
# deliver user input via send-keys into ADK's interactive input() loop.

ARG BASE_IMAGE=us-central1-docker.pkg.dev/ptone-misc/public-docker/scion-base:latest
FROM ${BASE_IMAGE}

USER root

# Create workspace directory
RUN mkdir -p /workspace && chown scion:scion /workspace
WORKDIR /workspace

# Install google-adk into a virtualenv so we get a clean pip environment
# without conflicting with system packages.
RUN python3 -m venv /opt/adk-venv && \
    /opt/adk-venv/bin/pip install --no-cache-dir google-adk

# Put the venv on PATH so `adk` CLI is available to all users.
ENV PATH="/opt/adk-venv/bin:${PATH}"

# Copy the agent source code and ensure Python can find the package.
COPY --chown=scion:scion . /opt/adk_scion_agent/adk_scion_agent/
ENV PYTHONPATH="/opt/adk_scion_agent:${PYTHONPATH}"

# Default command: run the ADK agent via the custom runner entrypoint.
# The custom runner supports --input for task delivery (which the released
# `adk run` CLI does not yet support). scion's runtime layer wraps this in
# `tmux new-session -s scion ...` and appends `--input <task>` when task_flag
# is configured.
CMD ["python", "-m", "adk_scion_agent"]
