# Deprecated Flags Migration Guide

This document provides a comprehensive migration path for all deprecated CLI flags in LocalAI.

## Overview

LocalAI periodically updates its CLI interface to improve usability and maintain consistency. This guide helps users migrate from deprecated flags to their modern equivalents.

## Deprecation Timeline

| Version | Action |
|---------|--------|
| v2.0.0  | Flag deprecated, warning shown |
| v2.5.0  | Warning includes migration instructions |
| v3.0.0  | Flag removed (planned) |

---

## Deprecated Flags Reference

### Backend Configuration

#### `--single-active-backend`

**Status:** Deprecated since v2.0.0  
**Removed in:** v3.0.0 (planned)  
**Replacement:** `--max-active-backends=1`

**Old Command:**
```bash
localai run --single-active-backend
```

**New Command:**
```bash
localai run --max-active-backends=1
```

**Behavior Differences:**
- No behavioral difference; both allow only one backend to run at a time
- The new flag is part of a more flexible backend management system

**Environment Variable:**
- Old: `SINGLE_ACTIVE_BACKEND=true`
- New: `MAX_ACTIVE_BACKENDS=1`

---

## Migration Checklist

- [ ] Replace `--single-active-backend` with `--max-active-backends=1`
- [ ] Update environment variables if used
- [ ] Test application after migration
- [ ] Remove deprecated flags before v3.0.0 release

---

## How to Find Deprecated Flags

Run the following to see all flags with deprecation warnings:

```bash
localai run --help | grep -i deprecated
```

## Reporting Issues

If you encounter issues during migration, please open an issue at:
https://github.com/mudler/LocalAI/issues

---

*Last updated: 2026-03-09*
