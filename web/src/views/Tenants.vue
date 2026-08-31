<script setup>
import { ref, onMounted } from 'vue'
import { api } from '../api'
import { t } from '../i18n'
import { formatBytes } from '../format'
import QuotaBar from '../components/QuotaBar.vue'

const tenants = ref([])
const plans = ref([])
const quotas = ref({})
const loading = ref(true)
const error = ref('')
const busy = ref(false)
const tab = ref('tenants')

const showTenantForm = ref(false)
const showPlanForm = ref(false)
const tenantForm = ref({ name: '', slug: '', plan_id: null })
const planForm = ref({
  name: '',
  description: '',
  max_sites: 0,
  max_databases: 0,
  max_ftp: 0,
  max_cronjobs: 0,
  disk_quota_mb: 0,
  traffic_quota_mb: 0,
  is_default: false,
})

const inputStyle = {
  borderColor: 'var(--line-axis)',
  background: 'var(--surface-page)',
  color: 'var(--ink-primary)',
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    const [tenantList, planList] = await Promise.all([
      api.get('/tenants?all=true'),
      api.get('/plans'),
    ])
    tenants.value = tenantList
    plans.value = planList

    // Der Verbrauch kommt je Mandant einzeln; ein Fehler bei einem darf die
    // Liste nicht leeren.
    const entries = await Promise.all(
      tenantList.map(async (tenant) => {
        try {
          return [tenant.id, await api.get(`/tenants/${tenant.id}/quota`)]
        } catch {
          return [tenant.id, null]
        }
      }),
    )
    quotas.value = Object.fromEntries(entries)
  } catch (err) {
    error.value = err.message
  } finally {
    loading.value = false
  }
}

async function createTenant() {
  busy.value = true
  error.value = ''
  try {
    await api.post('/tenants', {
      ...tenantForm.value,
      plan_id: tenantForm.value.plan_id || null,
    })
    showTenantForm.value = false
    tenantForm.value = { name: '', slug: '', plan_id: null }
    await load()
  } catch (err) {
    error.value = err.message
  } finally {
    busy.value = false
  }
}

async function setPlan(tenant, planId) {
  try {
    // 0 löst die Zuordnung — sonst ließe sie sich nie entfernen.
    await api.patch(`/tenants/${tenant.id}`, { plan_id: Number(planId) || 0 })
    await load()
  } catch (err) {
    error.value = err.message
  }
}

async function toggleSuspend(tenant) {
  try {
    await api.patch(`/tenants/${tenant.id}`, {
      status: tenant.status === 'active' ? 'suspended' : 'active',
    })
    await load()
  } catch (err) {
    error.value = err.message
  }
}

async function removeTenant(tenant) {
  if (!confirm(t('tenants.confirmDelete', { name: tenant.name }))) return
  try {
    await api.del(`/tenants/${tenant.id}`)
    await load()
  } catch (err) {
    error.value = err.message
  }
}

async function createPlan() {
  busy.value = true
  error.value = ''
  try {
    await api.post('/plans', planForm.value)
    showPlanForm.value = false
    planForm.value = {
      name: '',
      description: '',
      max_sites: 0,
      max_databases: 0,
      max_ftp: 0,
      max_cronjobs: 0,
      disk_quota_mb: 0,
      traffic_quota_mb: 0,
      is_default: false,
    }
    await load()
  } catch (err) {
    error.value = err.message
  } finally {
    busy.value = false
  }
}

async function removePlan(plan) {
  if (!confirm(t('plans.confirmDelete', { name: plan.name }))) return
  try {
    await api.del(`/plans/${plan.id}`)
    await load()
  } catch (err) {
    error.value = err.message
  }
}

function limitText(value, bytes) {
  if (!value || value <= 0) return '∞'
  return bytes ? formatBytes(value * 1024 * 1024) : String(value)
}

const planFields = [
  { key: 'max_sites', label: 'sites.title' },
  { key: 'max_databases', label: 'db.title' },
  { key: 'max_ftp', label: 'plans.ftp' },
  { key: 'max_cronjobs', label: 'cron.title' },
  { key: 'disk_quota_mb', label: 'plans.diskMB' },
  { key: 'traffic_quota_mb', label: 'plans.trafficMB' },
]

onMounted(load)
</script>

<template>
  <div class="fade-in px-8 py-6">
    <header class="mb-5 flex flex-wrap items-center justify-between gap-3">
      <h1 class="text-[18px] font-semibold tracking-tight">{{ t('tenants.title') }}</h1>

      <div class="flex items-center gap-2">
        <button
          v-for="key in ['tenants', 'plans']"
          :key="key"
          class="rounded-md border px-3 py-1.5 text-[12px]"
          :style="{
            borderColor: tab === key ? 'var(--series-1)' : 'var(--line-axis)',
            color: tab === key ? 'var(--series-1)' : 'var(--ink-secondary)',
          }"
          @click="tab = key"
        >
          {{ t(key === 'tenants' ? 'tenants.tab' : 'plans.tab') }}
        </button>

        <button
          class="rounded-md px-3 py-1.5 text-[13px] font-medium text-white"
          :style="{ background: 'var(--series-1)' }"
          @click="tab === 'tenants' ? (showTenantForm = !showTenantForm) : (showPlanForm = !showPlanForm)"
        >
          {{ tab === 'tenants' ? t('tenants.new') : t('plans.new') }}
        </button>
      </div>
    </header>

    <p v-if="error" class="mb-4 text-[13px]" :style="{ color: 'var(--status-critical)' }" role="alert">
      {{ error }}
    </p>

    <!-- Mandanten -->
    <template v-if="tab === 'tenants'">
      <form
        v-if="showTenantForm"
        class="mb-5 grid gap-3 rounded-lg border p-4 sm:grid-cols-3"
        :style="{ borderColor: 'var(--border-ring)', background: 'var(--surface-card)' }"
        @submit.prevent="createTenant"
      >
        <label class="block">
          <span class="mb-1 block text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
            {{ t('tenants.name') }}
          </span>
          <input v-model="tenantForm.name" required placeholder="Kunde Meier"
                 class="w-full rounded-md border px-3 py-2 text-[13px]" :style="inputStyle" />
        </label>
        <label class="block">
          <span class="mb-1 block text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
            {{ t('plans.tab') }}
          </span>
          <select v-model="tenantForm.plan_id" class="w-full rounded-md border px-3 py-2 text-[13px]" :style="inputStyle">
            <option :value="null">{{ t('tenants.noPlan') }}</option>
            <option v-for="plan in plans" :key="plan.id" :value="plan.id">{{ plan.name }}</option>
          </select>
        </label>
        <div class="flex items-end">
          <button type="submit" :disabled="busy"
                  class="rounded-md px-3 py-2 text-[13px] font-medium text-white disabled:opacity-60"
                  :style="{ background: 'var(--series-1)' }">
            {{ busy ? t('common.loading') : t('sites.create') }}
          </button>
        </div>
      </form>

      <p v-if="loading" class="text-[13px]" :style="{ color: 'var(--ink-muted)' }">
        {{ t('common.loading') }}
      </p>

      <div v-else class="grid gap-3 lg:grid-cols-2">
        <article
          v-for="tenant in tenants"
          :key="tenant.id"
          class="rounded-lg border p-4"
          :style="{ borderColor: 'var(--border-ring)', background: 'var(--surface-card)' }"
        >
          <header class="mb-3 flex items-start justify-between gap-3">
            <div class="min-w-0">
              <div class="flex items-center gap-2">
                <span class="truncate text-[14px] font-medium">{{ tenant.name }}</span>
                <span
                  v-if="tenant.status !== 'active'"
                  class="rounded px-1.5 py-0.5 text-[10px] uppercase"
                  :style="{
                    background: 'color-mix(in srgb, var(--status-critical) 15%, transparent)',
                    color: 'var(--status-critical)',
                  }"
                >
                  {{ t('tenants.suspended') }}
                </span>
              </div>
              <div class="text-[11px]" :style="{ color: 'var(--ink-muted)' }">{{ tenant.slug }}</div>
            </div>

            <select
              :value="tenant.plan_id || 0"
              class="rounded-md border px-2 py-1 text-[11px]"
              :style="inputStyle"
              @change="setPlan(tenant, $event.target.value)"
            >
              <option :value="0">{{ t('tenants.noPlan') }}</option>
              <option v-for="plan in plans" :key="plan.id" :value="plan.id">{{ plan.name }}</option>
            </select>
          </header>

          <div v-if="quotas[tenant.id]" class="space-y-2.5">
            <QuotaBar
              v-for="e in quotas[tenant.id].entries"
              :key="e.resource"
              :label="t('quota.' + e.resource)"
              :used="e.used"
              :limit="e.limit"
              :percent="e.percent"
              :bytes="e.bytes"
            />
          </div>

          <footer class="mt-3 flex gap-3 text-[11px]">
            <button class="underline" :style="{ color: 'var(--ink-secondary)' }"
                    @click="toggleSuspend(tenant)">
              {{ tenant.status === 'active' ? t('tenants.suspend') : t('tenants.resume') }}
            </button>
            <button class="underline" :style="{ color: 'var(--status-critical)' }"
                    @click="removeTenant(tenant)">
              {{ t('sites.delete') }}
            </button>
          </footer>
        </article>
      </div>
    </template>

    <!-- Pakete -->
    <template v-else>
      <form
        v-if="showPlanForm"
        class="mb-5 grid gap-3 rounded-lg border p-4 sm:grid-cols-4"
        :style="{ borderColor: 'var(--border-ring)', background: 'var(--surface-card)' }"
        @submit.prevent="createPlan"
      >
        <label class="block sm:col-span-2">
          <span class="mb-1 block text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
            {{ t('tenants.name') }}
          </span>
          <input v-model="planForm.name" required placeholder="Klein"
                 class="w-full rounded-md border px-3 py-2 text-[13px]" :style="inputStyle" />
        </label>

        <label v-for="field in planFields" :key="field.key" class="block">
          <span class="mb-1 block text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
            {{ t(field.label) }}
          </span>
          <input v-model.number="planForm[field.key]" type="number" min="0"
                 class="tabular w-full rounded-md border px-3 py-2 text-[13px]" :style="inputStyle" />
        </label>

        <div class="flex items-end gap-3 sm:col-span-2">
          <label class="flex items-center gap-2 pb-2 text-[12px]">
            <input v-model="planForm.is_default" type="checkbox" />
            {{ t('plans.isDefault') }}
          </label>
          <button type="submit" :disabled="busy"
                  class="rounded-md px-3 py-2 text-[13px] font-medium text-white disabled:opacity-60"
                  :style="{ background: 'var(--series-1)' }">
            {{ busy ? t('common.loading') : t('sites.create') }}
          </button>
        </div>

        <p class="text-[11px] sm:col-span-4" :style="{ color: 'var(--ink-muted)' }">
          {{ t('plans.zeroHint') }}
        </p>
      </form>

      <div
        v-if="plans.length"
        class="overflow-hidden rounded-lg border"
        :style="{ borderColor: 'var(--border-ring)', background: 'var(--surface-card)' }"
      >
        <table class="w-full text-left text-[13px]">
          <thead class="text-[12px]" :style="{ color: 'var(--ink-muted)' }">
            <tr class="border-b" :style="{ borderColor: 'var(--line-hairline)' }">
              <th class="px-4 py-2.5 font-normal">{{ t('tenants.name') }}</th>
              <th class="px-4 py-2.5 text-right font-normal">{{ t('sites.title') }}</th>
              <th class="px-4 py-2.5 text-right font-normal">{{ t('db.title') }}</th>
              <th class="px-4 py-2.5 text-right font-normal">{{ t('cron.title') }}</th>
              <th class="px-4 py-2.5 text-right font-normal">{{ t('quota.disk') }}</th>
              <th class="px-4 py-2.5 text-right font-normal">{{ t('quota.traffic') }}</th>
              <th class="px-4 py-2.5 text-right font-normal">{{ t('common.actions') }}</th>
            </tr>
          </thead>
          <tbody class="tabular">
            <tr
              v-for="plan in plans"
              :key="plan.id"
              class="border-b last:border-0"
              :style="{ borderColor: 'var(--line-hairline)' }"
            >
              <td class="px-4 py-2.5 font-medium">
                {{ plan.name }}
                <span v-if="plan.is_default" class="ml-1.5 text-[10px] uppercase"
                      :style="{ color: 'var(--status-good)' }">
                  {{ t('plans.default') }}
                </span>
              </td>
              <td class="px-4 py-2.5 text-right">{{ limitText(plan.max_sites, false) }}</td>
              <td class="px-4 py-2.5 text-right">{{ limitText(plan.max_databases, false) }}</td>
              <td class="px-4 py-2.5 text-right">{{ limitText(plan.max_cronjobs, false) }}</td>
              <td class="px-4 py-2.5 text-right">{{ limitText(plan.disk_quota_mb, true) }}</td>
              <td class="px-4 py-2.5 text-right">{{ limitText(plan.traffic_quota_mb, true) }}</td>
              <td class="px-4 py-2.5 text-right">
                <button class="text-[12px] underline" :style="{ color: 'var(--status-critical)' }"
                        @click="removePlan(plan)">
                  {{ t('sites.delete') }}
                </button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <p v-else class="text-[13px]" :style="{ color: 'var(--ink-muted)' }">{{ t('plans.empty') }}</p>
    </template>
  </div>
</template>
