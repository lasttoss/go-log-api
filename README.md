# API Logging System

## Overview
This document describes the API logging system that logs user activities via public and private APIs, stores logs in a file, and then pushes them to ClickHouse using Vector.

## Components

### 1. API Design
- **Public API**: Client-to-server communication.
- **Private API**: Server-to-server communication.

### 2. Logging Mechanism
Logs include:
- UserId
- Key
- Data
- Metadata
- CreatedAt

### 3. File Logging
Logs are written to a JSON file.

### 4. Vector.Dev Configuration
Vector.dev reads from the log file and sends data to ClickHouse.o 