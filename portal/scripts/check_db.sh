#!/bin/bash
set -e

PSQL="docker exec -i ai-portal-postgres psql -U postgres -d ai_proxy_portal -t"

run() {
    $PSQL -c "$1" 2>/dev/null | sed 's/^[[:space:]]*//;s/[[:space:]]*$//'
}

echo "========== DB Structure Check =========="

# 1. Core tables
echo ""
echo "--- Core Tables ---"
for tbl in users api_keys providers enterprises user_groups subscription_plans service_models user_subscriptions enterprise_members audit_logs usage_records orders invoices redemption_codes redemption_records system_settings user_devices verification_codes enterprise_invitations payment_gateways subscription_purchases balance_transactions exchange_rates announcements announcement_reads invitation_codes invitation_records provider_health_stats upstream_key_usage model_provider_routing enterprise_audit_log enterprise_member_role_log proxy_heartbeats proxy_instances upstream_keys; do
    cnt=$(run "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = 'public' AND table_name = '$tbl'" || echo "0")
    if [ "$cnt" = "1" ]; then
        echo "  [OK] $tbl"
    else
        echo "  [MISSING] $tbl"
    fi
done

# 2. Check user_groups fields
echo ""
echo "--- user_groups fields ---"
for col in id name description pricing_ratio model_permissions rate_limit_rpm status group_type billing_mode models rpm rph rp5h rpw rpmonth tpm tph tpd tpw tpmonth enterprise_id visibility is_default routing_track concurrent_limit parent_group_id created_at updated_at; do
    cnt=$(run "SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'user_groups' AND column_name = '$col'" || echo "0")
    if [ "$cnt" = "1" ]; then
        echo "  [OK] $col"
    else
        echo "  [MISSING] $col"
    fi
done

# 3. Check enterprises fields
echo ""
echo "--- enterprises fields ---"
for col in id name contact_email status quota_total quota_used billing_cycle credit_total credit_used credit_reset_at last_payment_amount last_payment_confirmed_at last_payment_confirmed_by max_admins admin_vacant_since deleted_at deleted_by delete_reason pricing_ratio description contact_name contact_phone max_members allowed_domains created_at updated_at; do
    cnt=$(run "SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'enterprises' AND column_name = '$col'" || echo "0")
    if [ "$cnt" = "1" ]; then
        echo "  [OK] $col"
    else
        echo "  [MISSING] $col"
    fi
done

# 4. Check service_models fields
echo ""
echo "--- service_models fields ---"
for col in id provider product_id api_model_name status input_price output_price cache_read_price aimodels_data capabilities show_in_store sort_order protected last_group_cleanup_at last_eol_notified_at hidden input_price_cache_hit input_price_cache_miss reasoning_price billing_type validation_status validation_result validated_at suggested_api_name avg_latency_ms success_rate priority created_at updated_at; do
    cnt=$(run "SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'service_models' AND column_name = '$col'" || echo "0")
    if [ "$cnt" = "1" ]; then
        echo "  [OK] $col"
    else
        echo "  [MISSING] $col"
    fi
done

# 5. Seed data counts
echo ""
echo "--- Seed Data ---"
echo "  providers: $(run 'SELECT COUNT(*) FROM providers')"
echo "  subscription_plans: $(run 'SELECT COUNT(*) FROM subscription_plans')"
echo "  user_groups: $(run 'SELECT COUNT(*) FROM user_groups')"
echo "  default groups: $(run "SELECT COUNT(*) FROM user_groups WHERE is_default = true" || echo "0")"
echo "  enterprises: $(run 'SELECT COUNT(*) FROM enterprises')"
echo "  service_models: $(run 'SELECT COUNT(*) FROM service_models')"
echo "  users (admins): $(run "SELECT COUNT(*) FROM users WHERE role = 'super_admin'")"

# 6. Check for duplicate subscription plans
echo ""
echo "--- Duplicate Check ---"
dup=$(run "SELECT name, COUNT(*) FROM subscription_plans GROUP BY name HAVING COUNT(*) > 1" || echo "")
if [ -z "$dup" ]; then
    echo "  [OK] No duplicate subscription plans"
else
    echo "  [WARN] Duplicate plans found:"
    echo "$dup" | sed 's/^/    /'
fi

# 7. Check service_models by provider
echo ""
echo "--- service_models by provider ---"
run "SELECT COALESCE(provider,'NULL'), COUNT(*) FROM service_models GROUP BY provider ORDER BY COUNT(*) DESC LIMIT 10" || echo "  (no data)"

# 8. Check FK constraints on api_keys
echo ""
echo "--- Foreign Key Constraints on api_keys ---"
run "SELECT tc.constraint_name, kcu.column_name, ccu.table_name AS foreign_table_name, ccu.column_name AS foreign_column_name FROM information_schema.table_constraints AS tc JOIN information_schema.key_column_usage AS kcu ON tc.constraint_name = kcu.constraint_name AND tc.table_schema = kcu.table_schema JOIN information_schema.constraint_column_usage AS ccu ON ccu.constraint_name = tc.constraint_name AND ccu.table_schema = tc.table_schema WHERE tc.constraint_type = 'FOREIGN KEY' AND tc.table_name='api_keys'" || echo "  (none found)"

# 9. Check schema_migrations versions
echo ""
echo "--- schema_migrations versions ---"
run "SELECT version FROM schema_migrations ORDER BY version" || echo "  (table empty or missing)"

echo ""
echo "========== Done =========="
