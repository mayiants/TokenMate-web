# System Test Plan - AI Portal + Proxy Go

> **Status:** ACTIVE
> **Created:** 2026-05-07T21:30:00Z

## Overview

End-to-end system tests for the AI Portal + Proxy platform. Tests run against live services (Portal :3001, Proxy :8080) using real API keys from the keypool.

## Test Environment

| Service | URL | Notes |
|---------|-----|-------|
| Portal | http://127.0.0.1:3001 | Go Portal (Gin + PostgreSQL + Redis) |
| Proxy | http://127.0.0.1:8080 | Go Proxy (connects to Portal Redis) |
| Admin | admin@example.com / <ADMIN_PASSWORD> | super_admin role |

## Test Categories

### 1. Health & Status
- [x] Portal health check (`GET /api/health`)
- [x] Proxy health check (`GET /health`)
- [x] System status (`GET /api/status`)

### 2. Authentication
- [x] Login with valid credentials
- [x] Login with invalid credentials (should fail)
- [x] Get current user info (`GET /api/auth/me`)
- [x] Register new user (requires verification code - skipped in automated test)
- [x] Change password
- [x] JWT token validation

### 3. Profile Management
- [x] Get user profile
- [x] Update profile (name, company)
- [x] Get profile data via web handler

### 4. API Keys
- [x] List API keys
- [x] Create API key (general type)
- [x] Get API key details
- [x] Update API key (name)
- [x] Delete API key (soft delete)

### 5. Billing & Usage
- [x] Get balance
- [x] Get usage summary (today + month)
- [x] Get recent usage (last N days)
- [x] Get usage by key
- [x] Get usage by model

### 6. Orders
- [x] List orders
- [x] Create order

### 7. Invoice Management
- [x] List invoice profiles (empty)
- [x] Create invoice profile (personal)
- [x] Create invoice profile (normal - VAT)
- [x] Create invoice profile (special - VAT with bank info)
- [x] Create invoice profile (proforma - international)
- [x] Update invoice profile
- [x] Delete invoice profile (unreferenced)
- [x] Delete invoice profile (referenced - should fail)
- [x] List invoice requests
- [x] Create invoice request (email delivery, amount in yuan)
- [x] Create invoice request (paper delivery with address)
- [x] Validation: invalid email, zero amount, missing fields

### 8. Address Management
- [x] List addresses (empty)
- [x] Create address (China)
- [x] Create address (international)
- [x] Update address
- [x] Set default address
- [x] Delete address

### 9. Subscription Plans
- [x] List public plans
- [x] List admin plans

### 10. Admin - Users
- [x] List all users
- [x] Get user details

### 11. Admin - Providers
- [x] List providers
- [x] Provider health status

### 12. Admin - Key Pool
- [x] List upstream keys (9 valid keys)
- [x] Key pool stats by provider

### 13. Admin - Models
- [x] List service models
- [x] List public models

### 14. Admin - Settings
- [x] Get system settings

### 15. Admin - Audit Logs
- [x] List audit logs

### 16. Admin - Groups
- [x] List user groups

### 17. Admin - Enterprise
- [x] List enterprises

### 18. Admin - Monitoring
- [x] Upstream key monitoring
- [x] Downstream key monitoring

### 19. Proxy Flow (End-to-End)
- [x] Create API key via portal
- [x] Send chat completion request to proxy with API key
- [x] Verify usage recorded in portal
- [x] Clean up test key

### 20. Checkin
- [x] Get check-in status
- [x] Daily check-in

### 21. Webhook (Proxy → Portal)
- [x] Usage batch submission (tested indirectly via proxy flow)

### 22. Sync (Proxy ← Portal)
- [x] Key pool sync (tested indirectly)
- [x] Model sync (tested indirectly)

## Test Script

Location: `scripts/system-test.sh`

Run: `bash scripts/system-test.sh`

## Constraints

- **No high-volume operations**: Upstream keys have limited quota
- **Single chat completion per test**: Use minimal tokens (max_tokens=5)
- **Clean up test data**: Delete test API keys after proxy flow test

## Expected Results

All tests should pass with HTTP 200/201 for success cases and appropriate error codes for failure cases.
