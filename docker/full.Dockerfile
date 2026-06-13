FROM devx-session-base:latest

# Modern Node.js (v22 LTS — the Ubuntu 24.04 package is v18)
RUN curl -fsSL https://deb.nodesource.com/setup_22.x | bash - \
    && apt-get install -y --no-install-recommends nodejs \
    && rm -rf /var/lib/apt/lists/*

# Go (latest stable)
ARG GO_VERSION=1.24.4
RUN curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-$(dpkg --print-architecture).tar.gz" \
    | tar -C /usr/local -xz
ENV PATH="/usr/local/go/bin:/root/go/bin:${PATH}"

# AI coding agents (installed globally via npm)
RUN npm install -g \
    @earendil-works/pi-coding-agent \
    @anthropic-ai/claude-code \
    @openai/codex

# OpenTofu (infrastructure as code)
RUN curl -fsSL https://get.opentofu.org/install-opentofu.sh | sh -s -- --install-method deb

# Supabase CLI
RUN npm install -g supabase

# AWS CLI v2
RUN apt-get update && apt-get install -y --no-install-recommends unzip \
    && curl -fsSL "https://awscli.amazonaws.com/awscli-exe-linux-$(uname -m).zip" -o /tmp/awscli.zip \
    && cd /tmp && unzip -q awscli.zip \
    && ./aws/install \
    && rm -rf /tmp/awscli.zip /tmp/aws /var/lib/apt/lists/*

WORKDIR /workspace
