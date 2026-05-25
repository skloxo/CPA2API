# Contributing to CPA2API-Manager

Thank you for your interest in making CPA2API-Manager even better! As a React + Vite + TypeScript application, we prioritize high-quality code, aesthetic UI/UX consistency, and strong type safety.

Please review this guide to learn how you can contribute effectively.

---

## 🛠️ Local Development Setup

### 1. Prerequisites
- **Node.js**: Version `18.x` or higher.
- **npm** or **pnpm**: A modern package manager.

### 2. Fork and Clone
1. Fork the repository on GitHub.
2. Clone your fork locally:
   ```bash
   git clone https://github.com/<your-username>/CPA2API-Manager.git
   cd CPA2API-Manager
   ```

### 3. Install Dependencies
Install all package dependencies:
```bash
npm install
```

### 4. Run Development Server
Start the local Vite development server:
```bash
npm run dev
```
Open [http://localhost:5173](http://localhost:5173) in your browser to interact with the panel interface.

---

## 💻 Frontend Coding Conventions

To ensure the panel remains clean, accessible, and maintainable, please follow these conventions:

### 1. TypeScript & Type Safety
- **Strict Typing**: Avoid using the `any` type. Define explicit types/interfaces for all components props, state, and API structures.
- **Type Files**: Place general shared types under `src/types/` or keep them local to the component if only used once.

### 2. Component Design & Architecture
- **Single Responsibility**: Every React component should do one thing well. Extract complex sub-components into smaller units.
- **File Structure**:
  - Keep reusable UI components in `src/components/ui/`.
  - Place feature-specific components inside directories corresponding to their modules (e.g., `src/components/dashboard/`).
- **CSS & Aesthetics**:
  - Use our established **CSS custom properties (CSS variables)** for colors, borders, shadows, and spacing.
  - Maintain the sleek **Dark Glassmorphism** theme. Avoid overriding colors with raw hex strings; use semantic variables (e.g., `var(--card-bg)`, `var(--accent-glow)`) to ensure theme harmony.

### 3. English Comments
- All developer comments, code explanations, and documentation annotations inside code files must be written in **English only**. If you encounter non-English comments in code sections you modify, please translate them.
- User-facing text and labels should remain in Chinese or leverage multi-language features if configured.

---

## 🧪 Testing, Linting & Formatting

Before staging your changes or committing:

### 1. ESLint Check
Ensure your code contains no linting errors or warnings:
```bash
npm run lint
```

### 2. Code Formatting
Format the files according to our Prettier specification:
```bash
npm run format
```

### 3. Production Build Compilation
Compile the project to confirm that Vite and the TypeScript compiler (`tsc`) can successfully build the application:
```bash
npm run build
```
This command compiles the files and outputs optimized static bundles into the `dist/` directory.

---

## 🚀 Pull Request Guidelines

1. **Branch Naming**: Branch off from `main`. Use a prefix that categorizes the change:
   - `feat/add-realtime-ttft-chart`
   - `fix/resolve-quota-sorting-bug`
   - `style/glassmorphism-border-polish`
2. **Conventional Commits**: Keep your commit messages clear and structured (e.g., `feat: introduce channels list sorting`, `fix: fix active token account display`).
3. **Open PR**: Push your branch and open a PR against the upstream `main` branch. Provide a comprehensive explanation of your updates, and attach screenshots or GIFs for any UI-related enhancements where applicable.

Thank you for contributing to the visual excellence and reliability of CPA2API-Manager!
