import { reactive } from "vue";
import { api } from "../api";

// Der Update-Stand steht an zwei Stellen: als Punkt in der Navigation und
// ausführlich in den Einstellungen. Ein gemeinsamer Speicher hält beide
// gleich und fragt den Server nur einmal.
export const update = reactive({
  loaded: false,
  checking: false,
  current: "",
  latest: "",
  available: false,
  notes: "",
  url: "",
  channel: "",
  error: "",
});

export async function checkUpdate(force = false) {
  update.checking = true;
  try {
    const res = await api.get(`/system/update${force ? "?refresh=1" : ""}`);
    Object.assign(update, res, { loaded: true });
  } catch (err) {
    // Ein nicht erreichbarer Kanal ist kein Fehler der Oberfläche. Der Grund
    // gehört sichtbar in die Anzeige, nicht in die Konsole.
    update.error = err.message;
    update.loaded = true;
  } finally {
    update.checking = false;
  }
}
