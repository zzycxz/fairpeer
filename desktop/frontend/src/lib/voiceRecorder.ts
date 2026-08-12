// voiceRecorder.ts — capture microphone audio as a WAV (base64 data URL).
//
// Why WAV (not MediaRecorder's webm): every audio-capable model we target
// (MiMo-V2.5, GLM-4-Voice, GPT-4o-audio) accepts wav, while webm support is
// spotty. Recording PCM directly via AudioContext + ScriptProcessorNode and
// encoding a WAV header gives us one universal format with zero server-side
// transcoding.
//
// ScriptProcessorNode is deprecated in favour of AudioWorklet, but AudioWorklet
// needs a separately-loadable worklet file (awkward in the Wails bundle);
// ScriptProcessor still works in every target webview (WebView2/WebKit) and the
// callback overhead is negligible for short voice-input clips. Swap to a
// worklet later if long recordings ever show main-thread jank.

export interface RecordingSession {
  /** Stops the mic and resolves to a "data:audio/wav;base64,..." data URL. */
  stop: () => Promise<string>;
}

export class VoiceRecorderError extends Error {
  readonly kind: "denied" | "notfound" | "unsupported" | "other";
  constructor(kind: VoiceRecorderError["kind"], message: string) {
    super(message);
    this.name = "VoiceRecorderError";
    this.kind = kind;
  }
}

export type MicPermission = "granted" | "denied" | "prompt" | "unknown";

/**
 * Queries the microphone permission state via the Permissions API when the
 * webview supports it. Returns "unknown" when the API is missing (some
 * webviews, e.g. older WebKitGTK) — in that case the caller falls through to
 * getUserMedia and lets it decide.
 *
 * "denied" here typically means the user or system has explicitly blocked mic
 * access for the app (Windows Settings → Privacy → Microphone, or macOS System
 * Settings → Privacy → Microphone). getUserMedia would fail anyway, so the
 * caller can skip straight to a "go to system settings" hint instead of a
 * confusing retry loop.
 */
export async function checkMicPermission(): Promise<MicPermission> {
  try {
    const nav = navigator as any;
    if (!nav.permissions?.query) return "unknown";
    // The "microphone" PermissionName isn't in every TS DOM lib version, so
    // cast through any rather than fight the type system.
    const status = await nav.permissions.query({ name: "microphone" });
    const state = status?.state;
    if (state === "granted" || state === "denied" || state === "prompt") return state;
    return "unknown";
  } catch {
    return "unknown";
  }
}

/**
 * Starts recording from the default microphone. Returns a session whose stop()
 * resolves to a base64 WAV data URL. Throw VoiceRecorderError on failure so the
 * caller can surface a precise hint (permission denied vs no mic vs unsupported).
 */
export interface RecordingOptions {
  /** Seconds of continuous silence before auto-stopping. 0 disables. Default 3. */
  silenceSeconds?: number;
  /** Hard cap on recording length in seconds. 0 disables. Default 60. */
  maxSeconds?: number;
  /** Fired when the session auto-stops (silence or max duration). The caller
   *  receives the recorded data URL and should run transcription, exactly like
   *  a manual stop. NOT fired on a manual stop. */
  onAutoStop?: (dataURL: string, reason: "silence" | "maxduration") => void;
}

export async function startRecording(opts: RecordingOptions = {}): Promise<RecordingSession> {
  if (typeof navigator === "undefined" || !navigator.mediaDevices?.getUserMedia) {
    throw new VoiceRecorderError("unsupported", "mediaDevices not available in this environment");
  }

  let stream: MediaStream;
  try {
    stream = await navigator.mediaDevices.getUserMedia({
      audio: { channelCount: 1, echoCancellation: true, noiseSuppression: true },
      video: false,
    });
  } catch (e: any) {
    const name = e?.name ?? "";
    if (name === "NotAllowedError" || name === "SecurityError") {
      throw new VoiceRecorderError("denied", "microphone permission denied");
    }
    if (name === "NotFoundError" || name === "OverconstrainedError") {
      throw new VoiceRecorderError("notfound", "no microphone device found");
    }
    throw new VoiceRecorderError("other", e?.message ?? String(e));
  }

  const AudioCtx: typeof AudioContext =
    (window as any).AudioContext || (window as any).webkitAudioContext;
  if (!AudioCtx) {
    stream.getTracks().forEach((t) => t.stop());
    throw new VoiceRecorderError("unsupported", "Web Audio API not supported");
  }

  const audioCtx = new AudioCtx();
  const sampleRate = audioCtx.sampleRate; // use the host rate; the WAV header records it
  const source = audioCtx.createMediaStreamSource(stream);
  // 4096-sample buffer, 1 in / 1 out. ScriptProcessor only fires onaudioprocess
  // when its output is connected through to audioCtx.destination — but wiring
  // the mic straight to destination echoes it back out the speakers (and can
  // howl with feedback). A zero-gain GainNode in between satisfies the
  // connection requirement so callbacks keep firing, while staying silent.
  const processor = audioCtx.createScriptProcessor(4096, 1, 1);
  const silentSink = audioCtx.createGain();
  silentSink.gain.value = 0;

  const silenceSeconds = opts.silenceSeconds ?? 3;
  const maxSeconds = opts.maxSeconds ?? 60;
  const onAutoStop = opts.onAutoStop;
  // VAD state. lastVoiceAt tracks the last moment we heard speech; if
  // (now - lastVoiceAt) exceeds silenceSeconds the user has stopped talking and
  // we auto-stop. RMS_THRESHOLD is a fixed, deliberately conservative floor: a
  // quiet room sits well below it, normal speech well above. Mics/environments
  // vary, but for short voice-input clips this fixed threshold is good enough;
  // a self-calibrating noise baseline can be layered on later if real use shows
  // false stops.
  const RMS_THRESHOLD = 0.012;
  const startedAt = performance.now();
  let lastVoiceAt = startedAt;

  const chunks: Float32Array[] = [];
  processor.onaudioprocess = (e: AudioProcessingEvent) => {
    if (stopped) return;
    const input = e.inputBuffer.getChannelData(0);
    // copy — the underlying buffer is reused by the browser each callback.
    chunks.push(new Float32Array(input));

    // Voice Activity Detection: RMS energy over this buffer. Only track voice
    // when silence auto-stop is enabled (cheap, but skip the loop otherwise).
    if (silenceSeconds > 0) {
      let sum = 0;
      for (let i = 0; i < input.length; i++) sum += input[i] * input[i];
      if (Math.sqrt(sum / input.length) > RMS_THRESHOLD) {
        lastVoiceAt = performance.now();
      }
    }

    // Auto-stop checks: silence timeout, then max-duration cap. Triggered at
    // most once (stopped guards re-entry). doStop is async; we fire-and-forget
    // and deliver the data URL via onAutoStop so the caller can transcribe.
    const now = performance.now();
    const silentFor = (now - lastVoiceAt) / 1000;
    const total = (now - startedAt) / 1000;
    let reason: "silence" | "maxduration" | null = null;
    if (silenceSeconds > 0 && silentFor >= silenceSeconds) reason = "silence";
    else if (maxSeconds > 0 && total >= maxSeconds) reason = "maxduration";
    if (reason) {
      void doStop().then((url) => onAutoStop?.(url, reason)).catch(() => { /* surfaced by stop() */ });
    }
  };
  source.connect(processor);
  processor.connect(silentSink);
  silentSink.connect(audioCtx.destination);

  // doStop is shared by manual stop() and auto-stop. `stopping` memoizes the
  // promise so a manual + auto race shares one stop (no double close); `stopped`
  // stops onaudioprocess from firing another auto-stop after the first.
  let stopped = false;
  let stopping: Promise<string> | null = null;
  const doStop = (): Promise<string> => {
    if (stopping) return stopping;
    stopped = true;
    stopping = (async () => {
      try {
        processor.onaudioprocess = null;
        processor.disconnect();
        source.disconnect();
        stream.getTracks().forEach((t) => t.stop());
        const flat = flattenChunks(chunks);
        const wav = encodeWav(flat, sampleRate);
        await audioCtx.close();
        return "data:audio/wav;base64," + arrayBufferToBase64(wav);
      } catch (e: any) {
        try { await audioCtx.close(); } catch { /* ignore */ }
        throw new VoiceRecorderError("other", e?.message ?? String(e));
      }
    })();
    return stopping;
  };

  return { stop: () => doStop() };
}

function flattenChunks(chunks: Float32Array[]): Float32Array {
  let total = 0;
  for (const c of chunks) total += c.length;
  const out = new Float32Array(total);
  let off = 0;
  for (const c of chunks) {
    out.set(c, off);
    off += c.length;
  }
  return out;
}

/** Encodes mono float32 PCM (-1..1) as a 16-bit PCM WAV ArrayBuffer. */
function encodeWav(samples: Float32Array, sampleRate: number): ArrayBuffer {
  const buffer = new ArrayBuffer(44 + samples.length * 2);
  const view = new DataView(buffer);
  writeString(view, 0, "RIFF");
  view.setUint32(4, 36 + samples.length * 2, true);
  writeString(view, 8, "WAVE");
  writeString(view, 12, "fmt ");
  view.setUint32(16, 16, true); // PCM chunk size
  view.setUint16(20, 1, true); // PCM format
  view.setUint16(22, 1, true); // mono
  view.setUint32(24, sampleRate, true);
  view.setUint32(28, sampleRate * 2, true); // byte rate
  view.setUint16(32, 2, true); // block align
  view.setUint16(34, 16, true); // bits per sample
  writeString(view, 36, "data");
  view.setUint32(40, samples.length * 2, true);
  let offset = 44;
  for (let i = 0; i < samples.length; i++) {
    let s = samples[i];
    if (s > 1) s = 1;
    else if (s < -1) s = -1;
    view.setInt16(offset, s < 0 ? s * 0x8000 : s * 0x7fff, true);
    offset += 2;
  }
  return buffer;
}

function writeString(view: DataView, offset: number, str: string): void {
  for (let i = 0; i < str.length; i++) view.setUint8(offset + i, str.charCodeAt(i));
}

function arrayBufferToBase64(buffer: ArrayBuffer): string {
  const bytes = new Uint8Array(buffer);
  let binary = "";
  const chunk = 0x8000; // avoid call-stack overflow on long audio
  for (let i = 0; i < bytes.length; i += chunk) {
    binary += String.fromCharCode.apply(null, Array.from(bytes.subarray(i, i + chunk)) as any);
  }
  return btoa(binary);
}
