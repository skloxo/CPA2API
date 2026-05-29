#!/usr/bin/env bash
#
# sync-upstream.sh - Git Subtree Synchronizer for CPA2API Monorepo
#
# This script automates:
# 1. Fetching upstream updates from the CPA-Manager repository (https://github.com/seakee/CPA-Manager.git).
# 2. Merging the upstream CPA-Manager updates into the CPA2API monorepo's 'web/' subdirectory using Git Subtree.
# 3. Compiling the Vite React control panel and deploying the generated single-file index.html directly to 'static/management.html'.
#

set -euo pipefail

# --- Configuration ---
UPSTREAM_REMOTE="upstream-manager"
UPSTREAM_URL="https://github.com/seakee/CPA-Manager.git"
UPSTREAM_BRANCH="main"
SUBTREE_PREFIX="web"

# Format colors for stdout
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m' # No Color

log_info() {
  echo -e "${BLUE}[INFO]${NC} $1"
}

log_success() {
  echo -e "${GREEN}[SUCCESS]${NC} $1"
}

log_warn() {
  echo -e "${YELLOW}[WARNING]${NC} $1"
}

log_error() {
  echo -e "${RED}[ERROR]${NC} $1"
}

# --- Step 1: Verify environment ---
log_info "Verifying Git working directory..."

if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  log_error "Not a git repository. Please run this script inside the repository root."
  exit 1
fi

# Check for uncommitted changes
if ! git diff-index --quiet HEAD --; then
  log_warn "You have uncommitted changes in your repository."
  git status --porcelain
  echo -e "\n${RED}Error: Git working directory must be clean before running subtree operations.${NC}"
  echo -e "Please commit or stash your changes and try again."
  exit 1
fi

# --- Step 2: Configure Remote ---
log_info "Checking upstream manager remote..."

if ! git remote | grep -q "^${UPSTREAM_REMOTE}$"; then
  log_info "Remote '${UPSTREAM_REMOTE}' not found. Adding it pointing to: ${UPSTREAM_URL}"
  git remote add "$UPSTREAM_REMOTE" "$UPSTREAM_URL"
else
  # Verify/update remote URL just in case
  CURRENT_URL=$(git remote get-url "$UPSTREAM_REMOTE")
  if [ "$CURRENT_URL" != "$UPSTREAM_URL" ]; then
    log_warn "Remote '${UPSTREAM_REMOTE}' points to '${CURRENT_URL}'. Updating to '${UPSTREAM_URL}'."
    git remote set-url "$UPSTREAM_REMOTE" "$UPSTREAM_URL"
  fi
fi

# --- Step 3: Pull Upstream Subtree Updates ---
log_info "Fetching latest updates from ${UPSTREAM_REMOTE}/${UPSTREAM_BRANCH}..."
git fetch "$UPSTREAM_REMOTE" "$UPSTREAM_BRANCH"

log_info "Executing Git Subtree pull to merge upstream updates into '${SUBTREE_PREFIX}/' folder..."
if git subtree pull --prefix="$SUBTREE_PREFIX" "$UPSTREAM_REMOTE" "$UPSTREAM_BRANCH" --squash -m "Merge upstream cpa-manager updates into web subtree"; then
  log_success "Git subtree synchronization completed successfully!"
else
  log_error "Subtree pull failed. There might be merge conflicts."
  log_warn "Please resolve conflicts manually, commit the changes, and proceed."
  exit 1
fi

# --- Step 4: Optional Build and Deploy Front-end ---
echo -e "\n----------------------------------------"
log_info "Would you like to build and deploy the React frontend to 'static/management.html' now?"
read -p "Enter choice [y/N]: " build_choice

if [[ "$build_choice" =~ ^[Yy]$ ]]; then
  log_info "Entering directory: ${SUBTREE_PREFIX}..."
  cd "$SUBTREE_PREFIX"

  # Detect package manager
  if [ -f "package-lock.json" ]; then
    INSTALL_CMD="npm ci"
  else
    INSTALL_CMD="npm install"
  fi

  log_info "Installing front-end dependencies using '${INSTALL_CMD}'..."
  if ! $INSTALL_CMD; then
    log_error "Failed to install frontend dependencies."
    exit 1
  fi

  log_info "Building Vite React project..."
  if ! npm run build; then
    log_error "Failed to compile the React control panel."
    exit 1
  fi

  # Deploy compiled single file
  DIST_FILE="dist/index.html"
  TARGET_FILE="../static/management.html"

  if [ -f "$DIST_FILE" ]; then
    log_info "Deploying compiled control panel asset to: ${TARGET_FILE}..."
    cp "$DIST_FILE" "$TARGET_FILE"
    
    # Commit updated static management file
    log_info "Committing compiled frontend static asset..."
    cd ..
    git add static/management.html
    git commit -m "chore(web): update compiled management control panel" || true
    
    log_success "Control panel compiled and deployed successfully!"
  else
    log_error "Build output '${DIST_FILE}' not found."
    exit 1
  fi
else
  log_info "Frontend build skipped. Subtree files are synchronized."
fi

log_success "All tasks completed!"
