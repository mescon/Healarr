# Contributing to Healarr

Thank you for your interest in contributing to Healarr! This document provides guidelines and instructions for contributing.

## Development Setup

### Prerequisites

- Go 1.25+
- Node.js 22+
- npm 10+

### Building from Source

```bash
# Clone the repository
git clone https://github.com/mescon/Healarr.git
cd Healarr

# Build frontend
cd frontend && npm ci && npm run build && cd ..

# Build backend
go build -o healarr ./cmd/server

# Run
./healarr
```

### Development Mode

```bash
# Terminal 1: Run backend
go run ./cmd/server

# Terminal 2: Run frontend with hot reload
cd frontend && npm run dev
```

The frontend dev server proxies API requests to the backend on port 3090.

## Code Style

### Go
- Follow standard Go formatting (`go fmt`)
- Use meaningful variable and function names
- Add comments for exported functions

### TypeScript/React
- Use TypeScript for all new code
- Follow existing component patterns
- Use Tailwind CSS for styling

## Pull Request Process

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/amazing-feature`)
3. Make your changes
4. Test your changes locally
5. Commit with clear messages (`git commit -m 'Add amazing feature'`)
6. Push to your fork (`git push origin feature/amazing-feature`)
7. Open a Pull Request

## Reporting Issues

When reporting issues, please include:
- Healarr version
- Operating system
- Steps to reproduce
- Expected vs actual behavior
- Relevant logs (from Config > Logs)

## License

By contributing, you agree that your contributions will be licensed under the GPL-3.0 License.
