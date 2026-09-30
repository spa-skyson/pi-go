#!/bin/bash
# test-memory-e2e.sh - E2E tests for pi-go memory system
# Usage: ./scripts/test-memory-e2e.sh

set -euo pipefail

# Working directory for tests
TEST_DIR=""
PASS=0
FAIL=0

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

log_pass() { echo -e "${GREEN}✓${NC} $1"; PASS=$((PASS + 1)); }
log_fail() { echo -e "${RED}✗${NC} $1"; FAIL=$((FAIL + 1)); }
log_info() { echo -e "${YELLOW}→${NC} $1"; }

cleanup() {
    if [[ -n "$TEST_DIR" && -d "$TEST_DIR" ]]; then
        rm -rf "$TEST_DIR"
    fi
}
trap cleanup EXIT

# ============================================
# Test 1: Memory Status
# ============================================
test_status() {
    log_info "Test: memory status"
    if pirate memory status &>/dev/null; then
        log_pass "memory status works"
    else
        log_fail "memory status failed"
    fi
}

# ============================================
# Test 2: Memory Init
# ============================================
test_init() {
    local dir=$(mktemp -d)
    log_info "Test: memory init"
    
    if pirate memory init "$dir" --wing test-e2e &>/dev/null; then
        log_pass "memory init works"
    else
        log_fail "memory init failed"
    fi
}

# ============================================
# Test 3: Memory Search
# ============================================
test_search() {
    log_info "Test: memory search"
    
    if pirate memory search "test" &>/dev/null; then
        log_pass "memory search works"
    else
        log_fail "memory search failed"
    fi
}

test_search_with_limit() {
    log_info "Test: memory search with limit"
    
    if pirate memory search "test" --limit 5 &>/dev/null; then
        log_pass "memory search with limit works"
    else
        log_fail "memory search with limit failed"
    fi
}

test_search_wing_filter() {
    log_info "Test: memory search with wing filter"
    
    if pirate memory search "test" --wing pi-go &>/dev/null; then
        log_pass "memory search wing filter works"
    else
        log_fail "memory search wing filter failed"
    fi
}

# ============================================
# Test 4: Memory Recent
# ============================================
test_recent() {
    log_info "Test: memory recent"
    
    # recent uses current directory's project palace
    # Use home directory which has the global palace
    if cd ~ && pirate memory recent &>/dev/null; then
        log_pass "memory recent works"
    else
        log_fail "memory recent failed"
    fi
    cd - >/dev/null
}

test_recent_with_limit() {
    log_info "Test: memory recent with limit"
    
    if cd ~ && pirate memory recent --limit 10 &>/dev/null; then
        log_pass "memory recent with limit works"
    else
        log_fail "memory recent with limit failed"
    fi
    cd - >/dev/null
}

test_recent_type_filter() {
    log_info "Test: memory recent with type filter"
    
    if cd ~ && pirate memory recent --type bugfix &>/dev/null; then
        log_pass "memory recent type filter works"
    else
        log_fail "memory recent type filter failed"
    fi
    cd - >/dev/null
}

# ============================================
# Test 5: Memory Wake-Up
# ============================================
test_wakeup() {
    log_info "Test: memory wake-up"
    
    if pirate memory wake-up &>/dev/null; then
        log_pass "memory wake-up works"
    else
        log_fail "memory wake-up failed"
    fi
}

test_wakeup_wing_filter() {
    log_info "Test: memory wake-up with wing filter"
    
    if pirate memory wake-up --wing pi-go &>/dev/null; then
        log_pass "memory wake-up wing filter works"
    else
        log_fail "memory wake-up wing filter failed"
    fi
}

# ============================================
# Test 6: Knowledge Graph
# ============================================
test_kg_query() {
    log_info "Test: memory kg query"
    
    if pirate memory kg query "test" &>/dev/null; then
        log_pass "memory kg query works"
    else
        log_fail "memory kg query failed"
    fi
}

test_kg_add() {
    log_info "Test: memory kg add"
    
    # Use unique IDs to avoid conflicts
    local id="test_$(date +%s)"
    
    if pirate memory kg add "$id" works_on project-x &>/dev/null; then
        log_pass "memory kg add works"
    else
        log_fail "memory kg add failed"
    fi
}

test_kg_timeline() {
    log_info "Test: memory kg timeline"
    
    if pirate memory kg timeline "test" &>/dev/null; then
        log_pass "memory kg timeline works"
    else
        log_fail "memory kg timeline failed"
    fi
}

# ============================================
# Test 7: Memory Mine
# ============================================
test_mine_files() {
    local dir=$(mktemp -d)
    log_info "Test: memory mine files"
    
    # Init palace
    pirate memory init "$dir" --wing test-e2e &>/dev/null
    
    # Create some test files
    echo "// Test file for memory mining" > "$dir/test.go"
    echo "# Test documentation" > "$dir/README.md"
    
    if pirate memory mine "$dir" --wing test-e2e &>/dev/null; then
        log_pass "memory mine files works"
    else
        log_fail "memory mine files failed"
    fi
}

# ============================================
# Test 8: Memory Model
# ============================================
test_model_status() {
    log_info "Test: memory model status"
    
    if pirate memory model status &>/dev/null; then
        log_pass "memory model status works"
    else
        log_fail "memory model status failed"
    fi
}

test_model_download_help() {
    log_info "Test: memory model download command"
    
    if pirate memory model download --help &>/dev/null; then
        log_pass "memory model download command available"
    else
        log_fail "memory model download command not available"
    fi
}

# ============================================
# Test 9: Empty Palace Scenarios
# ============================================
test_empty_palace_status() {
    log_info "Test: memory status with no DB"
    
    # Point to nonexistent path - should exit gracefully
    if pirate memory status --db /tmp/nonexistent_palace.db &>/dev/null; then
        log_pass "memory status handles missing DB gracefully"
    else
        log_fail "memory status should handle missing DB"
    fi
}

test_empty_palace_wakeup() {
    local dir=$(mktemp -d)
    log_info "Test: memory wake-up with empty palace"
    
    pirate memory init "$dir" --wing empty-test &>/dev/null
    
    # Should output "no palace context" message
    if pirate memory wake-up --db "$dir/.pirate/palace.db" 2>&1 | grep -qi "no palace\|context\|Add drawers"; then
        log_pass "memory wake-up handles empty palace"
    else
        log_fail "memory wake-up should handle empty palace"
    fi
}

# ============================================
# Test 10: JSON Output Mode
# ============================================
test_recent_json() {
    log_info "Test: memory recent JSON output"
    
    if cd ~ && pirate memory recent --json 2>/dev/null; then
        log_pass "memory recent JSON output works"
    else
        log_fail "memory recent JSON output failed"
    fi
    cd - >/dev/null
}

# ============================================
# Test 11: Mine Conversations
# ============================================
test_mine_conversations() {
    local dir=$(mktemp -d)
    log_info "Test: memory mine conversations"
    
    # Init palace
    pirate memory init "$dir" --wing test-e2e &>/dev/null
    
    # Create a test session file (JSONL format)
    mkdir -p "$dir/sessions"
    echo '{"messages":[{"role":"user","content":"test"}]}' > "$dir/sessions/test.jsonl"
    
    if pirate memory mine "$dir" --convos --wing test-e2e &>/dev/null; then
        log_pass "memory mine conversations works"
    else
        log_fail "memory mine conversations failed"
    fi
}

# ============================================
# Run All Tests
# ============================================
main() {
    echo "=========================================="
    echo "Pi-Go Memory System E2E Tests"
    echo "=========================================="
    echo ""
    echo "Working directory: $(pwd)"
    echo ""
    
    echo "--- Palace Status & Model ---"
    test_status
    test_model_status
    test_model_download_help
    test_empty_palace_status
    
    echo ""
    echo "--- Memory Search ---"
    test_search
    test_search_with_limit
    test_search_wing_filter
    
    echo ""
    echo "--- Memory Recent ---"
    test_recent
    test_recent_with_limit
    test_recent_type_filter
    test_recent_json
    
    echo ""
    echo "--- Memory Wake-Up ---"
    test_wakeup
    test_wakeup_wing_filter
    
    echo ""
    echo "--- Knowledge Graph ---"
    test_kg_query
    test_kg_add
    test_kg_timeline
    
    echo ""
    echo "--- Palace Initialization ---"
    test_init
    
    echo ""
    echo "--- Memory Mining ---"
    test_mine_files
    test_mine_conversations
    test_empty_palace_wakeup
    
    echo ""
    echo "=========================================="
    echo "Results: $PASS passed, $FAIL failed"
    echo "=========================================="
    
    if [[ $FAIL -gt 0 ]]; then
        exit 1
    fi
    exit 0
}

main "$@"
