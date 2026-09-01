<script setup>
import { ref, computed, onMounted } from 'vue'
import { api } from '../api'
import { t } from '../i18n'

const databases = ref([])
const selected = ref(null)
const tables = ref([])
const statement = ref('')
const result = ref(null)
const error = ref('')
const running = ref(false)
const loading = ref(true)

// Die zuletzt gelaufenen Anweisungen, damit sich eine wiederholen lässt, ohne
// sie erneut zu tippen. Nur in dieser Sitzung — SQL gehört nicht in den
// Speicher des Browsers, es steht regelmäßig etwas darin, was niemanden angeht.
const history = ref([])

const inputStyle = {
  borderColor: 'var(--line-axis)',
  background: 'var(--surface-page)',
  color: 'var(--ink-primary)',
}

const current = computed(() => databases.value.find((d) => d.id === selected.value))

async function load() {
  loading.value = true
  try {
    databases.value = await api.get('/databases')
    if (databases.value.length && selected.value === null) {
      await choose(databases.value[0].id)
    }
    error.value = ''
  } catch (err) {
    error.value = err.message
  } finally {
    loading.value = false
  }
}

async function choose(id) {
  selected.value = id
  tables.value = []
  result.value = null
  try {
    const res = await run('SHOW TABLES', { quiet: true })
    // SHOW TABLES liefert eine Spalte, deren Name die Datenbank enthält.
    tables.value = (res?.rows || []).map((row) => row[0]).filter(Boolean)
  } catch (err) {
    error.value = err.message
  }
}

async function run(sql, { quiet = false } = {}) {
  if (selected.value === null) return null
  running.value = true
  try {
    const res = await api.post(`/databases/${selected.value}/query`, { statement: sql })
    if (!quiet) {
      result.value = res
      // Dieselbe Anweisung nicht zweimal hintereinander in der Liste.
      history.value = [sql, ...history.value.filter((h) => h !== sql)].slice(0, 20)
    }
    error.value = ''
    return res
  } catch (err) {
    if (!quiet) result.value = null
    error.value = err.message
    throw err
  } finally {
    running.value = false
  }
}

async function submit() {
  const sql = statement.value.trim()
  if (!sql) return
  try {
    await run(sql)
    // Nach einer schreibenden Anweisung kann die Tabellenliste anders sein.
    if (!result.value?.has_result_set) await refreshTables()
  } catch {
    /* die Meldung steht schon in error */
  }
}

async function refreshTables() {
  try {
    const res = await run('SHOW TABLES', { quiet: true })
    tables.value = (res?.rows || []).map((row) => row[0]).filter(Boolean)
  } catch {
    /* die Tabellenliste ist Beiwerk; der Fehler der Anweisung zählt */
  }
}

// Ein Klick auf eine Tabelle zeigt ihren Anfang. Das ist, was man in neun von
// zehn Fällen will, und es tippt sich sonst jedes Mal neu.
function peek(table) {
  statement.value = `SELECT * FROM \`${table}\` LIMIT 50`
  submit()
}

function describe(table) {
  statement.value = `DESCRIBE \`${table}\``
  submit()
}

// Strg+Enter führt aus — im Textfeld ist Enter der Zeilenumbruch.
function onKey(event) {
  if ((event.metaKey || event.ctrlKey) && event.key === 'Enter') submit()
}

onMounted(load)
</script>

<template>
  <div class="fade-in px-8 py-6">
    <header class="mb-5 flex flex-wrap items-center justify-between gap-3">
      <h1 class="text-[18px] font-semibold tracking-tight">{{ t('sql.title') }}</h1>
      <select
        v-if="databases.length"
        :value="selected"
        class="rounded-md border px-3 py-1.5 text-[13px]"
        :style="inputStyle"
        @change="choose(Number($event.target.value))"
      >
        <option v-for="db in databases" :key="db.id" :value="db.id">{{ db.name }}</option>
      </select>
    </header>

    <p v-if="loading" class="text-[13px]" :style="{ color: 'var(--ink-muted)' }">
      {{ t('common.loading') }}
    </p>

    <p v-else-if="!databases.length" class="text-[13px]" :style="{ color: 'var(--ink-muted)' }">
      {{ t('db.empty') }}
    </p>

    <div v-else class="grid gap-5 lg:grid-cols-[220px_1fr]">
      <!-- Tabellen der gewählten Datenbank -->
      <aside
        class="rounded-lg border p-3"
        :style="{ borderColor: 'var(--border-ring)', background: 'var(--surface-card)' }"
      >
        <h2 class="mb-2 text-[12px] font-medium" :style="{ color: 'var(--ink-secondary)' }">
          {{ t('sql.tables') }}
        </h2>
        <p v-if="!tables.length" class="text-[12px]" :style="{ color: 'var(--ink-muted)' }">
          {{ t('sql.noTables') }}
        </p>
        <ul class="space-y-0.5">
          <li v-for="table in tables" :key="table" class="flex items-center justify-between gap-2">
            <button
              class="truncate text-left font-mono text-[12px] hover:underline"
              :title="table"
              @click="peek(table)"
            >
              {{ table }}
            </button>
            <button
              class="shrink-0 text-[11px] underline"
              :style="{ color: 'var(--ink-muted)' }"
              @click="describe(table)"
            >
              {{ t('sql.structure') }}
            </button>
          </li>
        </ul>
      </aside>

      <section>
        <form class="mb-3" @submit.prevent="submit">
          <textarea
            v-model="statement"
            rows="5"
            spellcheck="false"
            class="w-full rounded-md border px-3 py-2 font-mono text-[12px]"
            :style="inputStyle"
            :placeholder="t('sql.placeholder')"
            @keydown="onKey"
          ></textarea>
          <div class="mt-2 flex flex-wrap items-center gap-3">
            <button
              type="submit"
              :disabled="running"
              class="rounded-md px-4 py-2 text-[13px] font-medium text-white disabled:opacity-60"
              :style="{ background: 'var(--series-1)' }"
            >
              {{ running ? t('sql.running') : t('sql.run') }}
            </button>
            <span class="text-[11px]" :style="{ color: 'var(--ink-muted)' }">
              {{ t('sql.shortcut') }}
            </span>
            <span v-if="current" class="text-[11px]" :style="{ color: 'var(--ink-muted)' }">
              {{ t('sql.against', { name: current.name }) }}
            </span>
          </div>
        </form>

        <p
          v-if="error"
          class="mb-3 rounded-md border p-3 font-mono text-[12px] whitespace-pre-line"
          :style="{ borderColor: 'var(--status-critical)', color: 'var(--status-critical)' }"
          role="alert"
        >
          {{ error }}
        </p>

        <div v-if="result">
          <p class="mb-2 text-[12px]" :style="{ color: 'var(--ink-secondary)' }">
            <template v-if="result.has_result_set">
              {{ t('sql.rows', { count: result.rows.length, ms: result.duration_ms }) }}
            </template>
            <template v-else>
              {{ t('sql.affected', { count: result.rows_affected, ms: result.duration_ms }) }}
            </template>
          </p>
          <p v-if="result.warning" class="mb-2 text-[12px]" :style="{ color: 'var(--status-warning)' }">
            {{ result.warning }}
          </p>

          <div
            v-if="result.has_result_set && result.rows.length"
            class="overflow-x-auto rounded-lg border"
            :style="{ borderColor: 'var(--border-ring)', background: 'var(--surface-card)' }"
          >
            <table class="w-full text-left text-[12px]">
              <thead class="text-[11px]" :style="{ color: 'var(--ink-muted)' }">
                <tr class="border-b" :style="{ borderColor: 'var(--line-hairline)' }">
                  <th
                    v-for="col in result.columns"
                    :key="col"
                    class="px-3 py-2 font-normal whitespace-nowrap"
                  >
                    {{ col }}
                  </th>
                </tr>
              </thead>
              <tbody>
                <tr
                  v-for="(row, i) in result.rows"
                  :key="i"
                  class="border-b last:border-0"
                  :style="{ borderColor: 'var(--line-hairline)' }"
                >
                  <!-- null ist nicht der leere Text. Der Unterschied ist beim
                       Lesen einer Tabelle regelmäßig genau der Punkt. -->
                  <td v-for="(cell, j) in row" :key="j" class="px-3 py-1.5 align-top font-mono">
                    <span v-if="cell === null" :style="{ color: 'var(--ink-muted)' }">NULL</span>
                    <span v-else class="break-all whitespace-pre-wrap">{{ cell }}</span>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>

          <p
            v-else-if="result.has_result_set"
            class="text-[12px]"
            :style="{ color: 'var(--ink-muted)' }"
          >
            {{ t('sql.noRows') }}
          </p>
        </div>

        <div v-if="history.length" class="mt-5">
          <h2 class="mb-1 text-[12px] font-medium" :style="{ color: 'var(--ink-secondary)' }">
            {{ t('sql.history') }}
          </h2>
          <ul class="space-y-0.5">
            <li v-for="(entry, i) in history" :key="i">
              <button
                class="block w-full truncate text-left font-mono text-[11px] hover:underline"
                :style="{ color: 'var(--ink-muted)' }"
                :title="entry"
                @click="statement = entry"
              >
                {{ entry }}
              </button>
            </li>
          </ul>
        </div>
      </section>
    </div>
  </div>
</template>
