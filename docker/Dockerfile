FROM ubuntu:24.04

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    wget \
    git \
    build-essential \
    python3 \
    python3-pip \
    nodejs \
    npm \
    tmux \
    && pip3 install --break-system-packages tmuxp \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /workspace
