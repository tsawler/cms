# Content Management System

An embeddable content management system for Go web applications.

## Documentation

- [Architecture](docs/ARCHITECTURE.md) - Overview of the system architecture
- [Quick Start](QUICKSTART.md) - Getting started guide
- [Design Principles](DESIGN.md) - Core design concepts  

## Packages

- [admin](admin/) - Admin interface components
- [auth](auth/) - Authentication and authorization  
- [media](media/) - Media library handling
- [render](render/) - Template rendering system
- [snippets](snippets/) - Content blocks and section presets
- [content](content/) - Page and post content management

## Getting Started

```go
// Import the CMS package
import "github.com/tsawler/cms"

// Configure CMS with your database
cfg := cms.Config{
    DB: dbPool,
    TemplateFS: templates,
}

c, err := cms.New(cfg)
if err != nil { 
    // handle error
}

// Run migrations
if err := c.Migrate(ctx); err != nil {
    // handle error
}
```

See [QUICKSTART.md](QUICKSTART.md) for full details.