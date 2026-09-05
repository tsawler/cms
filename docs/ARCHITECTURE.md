# CMS Architecture

This document describes the architecture and organization of the Content Management System.

## Package Structure

The CMS is organized into several core packages:

### Core Packages
- **admin/** - Admin interface components and handlers
- **auth/** - Authentication and authorization logic  
- **media/** - Media library management and storage handling
- **render/** - Template rendering and content presentation
- **snippets/** - Content block snippets and section presets
- **content/** - Page and post content management
- **editor/** - In-place editor functionality
- **search/** - Search indexing and retrieval

### Internal Packages  
- **internal/dialect/** - Database dialect handling (PostgreSQL, MySQL)
- **internal/sqldb/** - SQL database abstraction layer
- **internal/sessionstore/** - Session storage implementation
- **internal/redisstore/** - Redis session store implementation

## Key Components

The CMS is built around several core concepts:
1. **Database Abstraction** - Supports PostgreSQL, MySQL, and MariaDB through dialect pattern
2. **Template System** - Integrates with Go's html/template package
3. **Media Management** - Handles uploads, processing, and storage via S3-compatible interface
4. **Content Workflow** - Draft/publish system with version control
5. **Editor Interface** - Inline editing capabilities with rich text tools

## Data Flow

1. HTTP requests are handled by the main `cms` package
2. Authentication is managed through the `auth` package
3. Content and media access go through their respective packages  
4. Template rendering uses the `render` package
5. All database operations use the dialect abstraction layer in `internal`

## Testing Strategy

All tests are kept adjacent to source files with `_test.go` suffixes as per Go conventions.