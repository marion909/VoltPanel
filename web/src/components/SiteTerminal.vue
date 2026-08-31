<script setup>
import { ref, onMounted, onBeforeUnmount } from 'vue'
import { api } from '../api'
import { t } from '../i18n'

const props = defineProps({ siteId: { type: Number, required: true } })

const host = ref(null)
const status = ref('connecting')
const message = ref('')

let term = null
let fit = null
let socket = null
let observer = null

// xterm wird erst geladen, wenn dieser Reiter geöffnet wird. Es ist so groß wie
// der Rest der Oberfläche zusammen — jede andere Seite würde sonst dafür
// mitbezahlen.
onMounted(async () => {
  const [{ Terminal }, { FitAddon }] = await Promise.all([
    import('@xterm/xterm'),
    import('@xterm/addon-fit'),
    import('@xterm/xterm/css/xterm.css'),
  ])

  const style = getComputedStyle(document.documentElement)
  term = new Terminal({
    fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
    fontSize: 13,
    cursorBlink: true,
    scrollback: 5000,
    theme: {
      background: style.getPropertyValue('--surface-sunken').trim() || '#111318',
      foreground: style.getPropertyValue('--ink-primary').trim() || '#e6e8ea',
    },
  })
  fit = new FitAddon()
  term.loadAddon(fit)
  term.open(host.value)
  fit.fit()

  connect()

  // Die Shell muss die Fenstergröße kennen, sonst bricht alles um, was mit
  // Spalten rechnet — top, less, vim.
  observer = new ResizeObserver(() => {
    if (!fit || !term) return
    fit.fit()
    send({ resize: { cols: term.cols, rows: term.rows } })
  })
  observer.observe(host.value)
})

function connect() {
  socket = api.socket(`/sites/${props.siteId}/terminal?cols=${term.cols}&rows=${term.rows}`)
  socket.binaryType = 'arraybuffer'

  socket.onopen = () => {
    status.value = 'open'
    term.focus()
  }

  socket.onmessage = (event) => {
    // Binär ist Ausgabe der Shell, Text ist eine Meldung des Panels.
    if (typeof event.data === 'string') {
      const payload = JSON.parse(event.data)
      if (payload.closed) closed(payload.closed)
      return
    }
    term.write(new Uint8Array(event.data))
  }

  socket.onclose = () => closed(t('term.disconnected'))
  socket.onerror = () => closed(t('term.failed'))

  // Tastatureingaben gehen roh und binär hinaus; Steuerung als JSON-Text.
  term.onData((data) => {
    if (socket?.readyState === WebSocket.OPEN) {
      socket.send(new TextEncoder().encode(data))
    }
  })
}

function send(payload) {
  if (socket?.readyState === WebSocket.OPEN) socket.send(JSON.stringify(payload))
}

function closed(reason) {
  if (status.value === 'closed') return
  status.value = 'closed'
  message.value = reason
  term?.write(`\r\n\x1b[2m— ${reason} —\x1b[0m\r\n`)
}

function restart() {
  status.value = 'connecting'
  message.value = ''
  term.reset()
  connect()
}

onBeforeUnmount(() => {
  observer?.disconnect()
  socket?.close()
  term?.dispose()
})
</script>

<template>
  <div>
    <div class="mb-2 flex items-center justify-between gap-3">
      <p class="text-[11px]" :style="{ color: 'var(--ink-muted)' }">{{ t('term.hint') }}</p>
      <button
        v-if="status === 'closed'"
        class="rounded-md px-3 py-1 text-[12px] font-medium text-white"
        :style="{ background: 'var(--series-1)' }"
        @click="restart"
      >
        {{ t('term.reconnect') }}
      </button>
    </div>

    <div
      ref="host"
      class="h-[28rem] overflow-hidden rounded-lg border p-2"
      :style="{ borderColor: 'var(--border-ring)', background: 'var(--surface-sunken)' }"
    ></div>

    <p v-if="message" class="mt-2 text-[12px]" :style="{ color: 'var(--ink-muted)' }" role="status">
      {{ message }}
    </p>
  </div>
</template>
