# Contributing to CPA2API

Thank you for your interest in contributing to CPA2API! We welcome contributions of all forms, including bug reports, feature requests, documentation improvements, and pull requests.

Please take a moment to review this guide to ensure a smooth and productive collaboration process.

---

## 🛠️ Local Development Setup

### 1. Prerequisites
- **Go**: Version `1.26` or higher.
- **Git**: Installed and configured.
- **Docker** (Optional, for running tests and isolated environments): Docker Compose.

### 2. Fork and Clone
1. Fork this repository on GitHub.
2. Clone your fork locally:
   ```bash
   git clone https://github.com/<your-username>/CPA2API.git
   cd CPA2API
   ```

### 3. Initialize & Install Dependencies
Download required Go modules:
```bash
go mod download
```

### 4. Local Configuration
Copy the template configuration file to create your local config:
```bash
cp config.example.yaml config.yaml
```
Modify `config.yaml` to specify your local development ports, upstream credentials, and other runtime options.

> [!CAUTION]
> Never commit your production credentials or secrets (such as those in `config.yaml` or `auths/` directory) to Git!

---

## 💻 Coding Conventions

To maintain a clean and reliable codebase, please adhere to the following rules:

1. **Keep It Simple & Sweet (KISS)**: Keep your logic clear, concise, and focused. Avoid unnecessary complexity or speculative generalization.
2. **English Comments**: All comments in the code must be written in **English only**. If you are modifying code that contains Chinese or other non-English comments, please translate them to English as you edit.
3. **Format & Style**:
   - Always run `gofmt` to format your changes before committing:
     ```bash
     gofmt -w .
     ```
   - Keep Go imports styled cleanly (standard library imports separated from third-party libraries).
4. **No `log.Fatal`**: Avoid calling `log.Fatal` or `log.Fatalf` in API middleware or HTTP handlers, as this terminates the server process. Instead, return errors and handle them with appropriate HTTP status codes, and use logrus for structured logging.
5. **Defer Error Handling**: Wrap potential errors when calling deferred functions (e.g. closing files or bodies):
   ```go
   defer func() {
       if err := file.Close(); err != nil {
           log.Errorf("failed to close file: %v", err)
       }
   }()
   ```
6. **Shadowed Variables**: Watch out for variable shadowing, especially for `err`. Use distinctive variable names where necessary.

---

## 🧪 Testing and Verification

Before submitting any code changes, you **must** verify that your code compiles and passes all tests:

### 1. Verification of Compilation
Verify that the server builds successfully:
```bash
go build -o test-output ./cmd/server && rm test-output
```

### 2. Running Unit & Integration Tests
Run all unit tests to ensure no regressions have been introduced:
```bash
go test ./...
```
To run a specific test with verbose output:
```bash
go test -v -run TestName ./path/to/package
```

---

## 🚀 Pull Request Workflow

1. **Create a Branch**: Create a descriptive branch name from `main`:
   ```bash
   git checkout -b feat/add-heartbeat-handling
   ```
2. **Implement & Test**: Write your code, add necessary tests, format your changes, and make sure everything compiles and runs successfully.
3. **Commit Messages**: We recommend using Conventional Commits. For example:
   - `feat: support customized tool calling XML rendering`
   - `fix: resolve heartbeat SSE read timeout on Nginx`
   - `docs: update setup steps in README`
4. **Submit PR**: Push your branch to your GitHub fork and open a Pull Request against the upstream `main` branch. Provide a clear description of the problem solved or the feature added in your PR description.

Thank you again for helping to improve CPA2API!
