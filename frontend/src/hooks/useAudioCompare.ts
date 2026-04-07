import { useState, useCallback, useRef } from "react";
import type { AnalysisResult } from "@/types/api";
import { analyzeAudioFile, analyzeAudioArrayBuffer, type AnalysisProgress } from "@/lib/flac-analysis";
import { loadAudioAnalysisPreferences } from "@/lib/audio-analysis-preferences";
import { toastWithSound as toast } from "@/lib/toast-with-sound";
import { logger } from "@/lib/logger";
import { buildComparisonPrompt, compareResults, type MetricComparison } from "@/lib/audio-compare";

interface ProgressState {
    percent: number;
    message: string;
}

const DEFAULT_PROGRESS: ProgressState = { percent: 0, message: "Preparing..." };

export interface FileSlot {
    result: AnalysisResult | null;
    analyzing: boolean;
    progress: ProgressState;
    filePath: string;
    samples: Float32Array | null;
}

const emptySlot: FileSlot = {
    result: null,
    analyzing: false,
    progress: DEFAULT_PROGRESS,
    filePath: "",
    samples: null,
};

interface CancelToken { cancelled: boolean }

function fileNameFromPath(p: string): string {
    const parts = p.split(/[/\\]/);
    return parts[parts.length - 1] || p;
}

async function base64ToArrayBuffer(base64: string): Promise<ArrayBuffer> {
    const clean = base64.includes(",") ? base64.split(",")[1] : base64;
    const binary = atob(clean);
    const bytes = new Uint8Array(binary.length);
    for (let i = 0; i < binary.length; i++) {
        bytes[i] = binary.charCodeAt(i);
    }
    return bytes.buffer;
}

export function useAudioCompare() {
    const [slotA, setSlotA] = useState<FileSlot>({ ...emptySlot });
    const [slotB, setSlotB] = useState<FileSlot>({ ...emptySlot });
    const [comparisons, setComparisons] = useState<MetricComparison[] | null>(null);
    const [aiVerdict, setAiVerdict] = useState<string | null>(null);
    const [aiLoading, setAiLoading] = useState(false);

    const tokenARef = useRef<CancelToken | null>(null);
    const tokenBRef = useRef<CancelToken | null>(null);

    const setSlot = useCallback((slot: "A" | "B") => slot === "A" ? setSlotA : setSlotB, []);
    const tokenRef = useCallback((slot: "A" | "B") => slot === "A" ? tokenARef : tokenBRef, []);

    const updateComparisons = useCallback((a: AnalysisResult | null, b: AnalysisResult | null) => {
        if (a && b) {
            setComparisons(compareResults(a, b));
        } else {
            setComparisons(null);
        }
        setAiVerdict(null);
    }, []);

    const analyzeSlot = useCallback(async (slot: "A" | "B", file: File) => {
        const setter = setSlot(slot);
        const tRef = tokenRef(slot);

        if (tRef.current) tRef.current.cancelled = true;
        const token: CancelToken = { cancelled: false };
        tRef.current = token;

        setter(prev => ({ ...prev, analyzing: true, progress: { percent: 1, message: "Reading file..." }, result: null, filePath: file.name, samples: null }));

        try {
            const prefs = loadAudioAnalysisPreferences();
            const payload = await analyzeAudioFile(file, {
                fftSize: prefs.fftSize,
                windowFunction: prefs.windowFunction,
            }, (p: AnalysisProgress) => {
                if (token.cancelled) return;
                setter(prev => ({ ...prev, progress: { percent: Math.round(p.percent), message: p.message } }));
            }, () => token.cancelled);

            if (token.cancelled) return;

            setter(prev => ({ ...prev, analyzing: false, result: payload.result, samples: payload.samples }));

            if (slot === "A") {
                setSlotB(prev => { updateComparisons(payload.result, prev.result); return prev; });
            } else {
                setSlotA(prev => { updateComparisons(prev.result, payload.result); return prev; });
            }
        } catch (err) {
            if (err instanceof Error && err.message === "Analysis cancelled") return;
            const msg = err instanceof Error ? err.message : "Analysis failed";
            logger.error(`Compare slot ${slot} error: ${msg}`);
            toast.error("Analysis Failed", { description: msg });
            setter(prev => ({ ...prev, analyzing: false, progress: DEFAULT_PROGRESS }));
        }
    }, [setSlot, tokenRef, updateComparisons]);

    const analyzeSlotPath = useCallback(async (slot: "A" | "B", filePath: string) => {
        const setter = setSlot(slot);
        const tRef = tokenRef(slot);

        if (tRef.current) tRef.current.cancelled = true;
        const token: CancelToken = { cancelled: false };
        tRef.current = token;

        setter(prev => ({ ...prev, analyzing: true, progress: { percent: 1, message: "Reading file from disk..." }, result: null, filePath, samples: null }));

        try {
            const prefs = loadAudioAnalysisPreferences();
            const readFileAsBase64 = (window as any)?.go?.main?.App?.ReadFileAsBase64 as ((path: string) => Promise<string>) | undefined;
            if (!readFileAsBase64) throw new Error("ReadFileAsBase64 backend method is unavailable");

            const base64Data = await readFileAsBase64(filePath);
            if (token.cancelled) return;

            const arrayBuffer = await base64ToArrayBuffer(base64Data);
            if (token.cancelled) return;

            const fileName = fileNameFromPath(filePath);
            const payload = await analyzeAudioArrayBuffer({
                fileName,
                fileSize: arrayBuffer.byteLength,
                arrayBuffer,
            }, {
                fftSize: prefs.fftSize,
                windowFunction: prefs.windowFunction,
            }, (p: AnalysisProgress) => {
                if (token.cancelled) return;
                setter(prev => ({ ...prev, progress: { percent: Math.round(10 + p.percent * 0.9), message: p.message } }));
            }, () => token.cancelled);

            if (token.cancelled) return;

            setter(prev => ({ ...prev, analyzing: false, result: payload.result, samples: payload.samples }));

            if (slot === "A") {
                setSlotB(prev => { updateComparisons(payload.result, prev.result); return prev; });
            } else {
                setSlotA(prev => { updateComparisons(prev.result, payload.result); return prev; });
            }
        } catch (err) {
            if (err instanceof Error && err.message === "Analysis cancelled") return;
            const msg = err instanceof Error ? err.message : "Analysis failed";
            logger.error(`Compare slot ${slot} path error: ${msg}`);
            toast.error("Analysis Failed", { description: msg });
            setter(prev => ({ ...prev, analyzing: false, progress: DEFAULT_PROGRESS }));
        }
    }, [setSlot, tokenRef, updateComparisons]);

    const requestAIComparison = useCallback(async () => {
        if (!slotA.result || !slotB.result) return;
        setAiLoading(true);
        setAiVerdict(null);
        try {
            const compareAI = (window as any)?.go?.main?.App?.CompareAudioAI as ((prompt: string) => Promise<string>) | undefined;
            if (!compareAI) {
                throw new Error("CompareAudioAI backend method is unavailable. Please rebuild the app with 'wails dev' or 'wails build'.");
            }
            const prompt = buildComparisonPrompt(slotA.result, slotB.result);
            const verdict = await compareAI(prompt);
            setAiVerdict(verdict);
        } catch (err: unknown) {
            console.error("AI comparison raw error:", err);
            console.error("Error type:", typeof err);
            console.error("Error stringified:", JSON.stringify(err));
            let msg: string;
            if (err instanceof Error) {
                msg = err.message;
            } else if (typeof err === "string") {
                msg = err;
            } else if (err && typeof err === "object" && "message" in err) {
                msg = String((err as { message: unknown }).message);
            } else {
                msg = String(err);
            }
            logger.error(`AI comparison error: ${msg}`);
            toast.error("AI Comparison Failed", { description: msg });
        } finally {
            setAiLoading(false);
        }
    }, [slotA.result, slotB.result]);

    const clearAll = useCallback(() => {
        if (tokenARef.current) tokenARef.current.cancelled = true;
        if (tokenBRef.current) tokenBRef.current.cancelled = true;
        tokenARef.current = null;
        tokenBRef.current = null;
        setSlotA({ ...emptySlot });
        setSlotB({ ...emptySlot });
        setComparisons(null);
        setAiVerdict(null);
        setAiLoading(false);
    }, []);

    return {
        slotA,
        slotB,
        comparisons,
        aiVerdict,
        aiLoading,
        analyzeSlot,
        analyzeSlotPath,
        requestAIComparison,
        clearAll,
    };
}
