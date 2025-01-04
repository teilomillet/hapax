---
layout: page
title: 5-Minute Setup
parent: Getting Started
nav_order: 1
---

# 5-Minute Setup

This guide will get you running with Hapax in under 5 minutes using Docker.

{: .note }
> **Prerequisites**
> - Docker installed
> - API key from any supported provider (OpenAI, Anthropic, etc.)

## 1. Run Hapax

Copy and run this command, replacing `your_key` with your API key:

```bash
docker run -p 8080:8080 \
  -e OPENAI_API_KEY=your_key \
  teilomillet/hapax:latest
```

## 2. Verify Installation

Test that Hapax is running:

```bash
curl http://localhost:8080/health
# Expected: {"status":"ok"}
```

## 3. Make Your First Request

Send a test completion request:

```bash
curl -X POST http://localhost:8080/v1/completions \
  -H "Content-Type: application/json" \
  -d '{"messages":[{"role":"user","content":"Hello"}]}'
```