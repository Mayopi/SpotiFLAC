import { useState, useCallback, useRef, useEffect, type ChangeEvent, type DragEvent, type CSSProperties } from "react";
import { Button } from "@/components/ui/button";
import { Progress } from "@/components/ui/progress";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Upload, Sparkles, Trash2, Loader2 } from "lucide-react";
import { SpectrumVisualization } from "@/components/SpectrumVisualization";
import { useAudioCompare, type FileSlot } from "@/hooks/useAudioCompare";
import { toastWithSound as toast } from "@/lib/toast-with-sound";
import { SelectFile } from "../../wailsjs/go/main/App";
import { OnFileDrop, OnFileDropOff } from "../../wailsjs/runtime/runtime";
import type { MetricComparison } from "@/lib/audio-compare";

const SUPPORTED_AUDIO_EXTENSIONS = [".flac", ".mp3", ".m4a", ".aac"];
const SUPPORTED_AUDIO_ACCEPT = [
    ".flac", ".mp3", ".m4a", ".aac",
    "audio/flac", "audio/x-flac", "audio/mpeg", "audio/mp3",
    "audio/mp4", "audio/x-m4a", "audio/aac", "audio/aacp",
].join(",");

function isSupportedAudioPath(filePath: string): boolean {
    const normalized = filePath.toLowerCase();
    return SUPPORTED_AUDIO_EXTENSIONS.some((ext) => normalized.endsWith(ext));
}

function isSupportedAudioFile(file: File): boolean {
    const normalizedName = file.name.toLowerCase();
    const normalizedType = file.type.toLowerCase();
    return (
        SUPPORTED_AUDIO_EXTENSIONS.some((ext) => normalizedName.endsWith(ext)) ||
        ["audio/flac", "audio/x-flac", "audio/mpeg", "audio/mp3", "audio/mp4", "audio/x-m4a", "audio/aac", "audio/aacp"].includes(normalizedType)
    );
}

function fileNameFromPath(filePath: string): string {
    const parts = filePath.split(/[/\\]/);
    return parts[parts.length - 1] || filePath;
}

function WinnerBadge({ winner, side }: { winner: MetricComparison["winner"]; side: "A" | "B" }) {
    if (winner === "na") return null;
    if (winner === "tie") return <span className="text-xs text-muted-foreground">Tie</span>;
    if (winner === side) return <span className="text-xs font-semibold text-green-500">Winner</span>;
    return null;
}

interface FileDropZoneProps {
    slot: "A" | "B";
    fileSlot: FileSlot;
    onFile: (slot: "A" | "B", file: File) => void;
    onPath: (slot: "A" | "B", path: string) => void;
}

function FileDropZone({ slot, fileSlot, onFile, onPath }: FileDropZoneProps) {
    const [isDragging, setIsDragging] = useState(false);
    const fileInputRef = useRef<HTMLInputElement>(null);

    const handleSelectFile = useCallback(async () => {
        try {
            const filePath = await SelectFile();
            if (!filePath) return;
            if (!isSupportedAudioPath(filePath)) {
                toast.error("Invalid File Type", { description: "Please select a FLAC, MP3, M4A, or AAC file" });
                return;
            }
            onPath(slot, filePath);
        } catch {
            fileInputRef.current?.click();
        }
    }, [slot, onPath]);

    const handleInputChange = useCallback((e: ChangeEvent<HTMLInputElement>) => {
        const file = e.target.files?.[0];
        if (!file) return;
        if (!isSupportedAudioFile(file)) {
            toast.error("Invalid File Type", { description: "Please select a FLAC, MP3, M4A, or AAC file" });
            return;
        }
        onFile(slot, file);
        e.target.value = "";
    }, [slot, onFile]);

    const handleHtmlDrop = useCallback((e: DragEvent<HTMLDivElement>) => {
        e.preventDefault();
        setIsDragging(false);
        const file = e.dataTransfer.files?.[0];
        if (!file) return;
        if (!isSupportedAudioFile(file)) {
            toast.error("Invalid File Type", { description: "Please select a FLAC, MP3, M4A, or AAC file" });
            return;
        }
        onFile(slot, file);
    }, [slot, onFile]);

    if (fileSlot.analyzing) {
        return (
            <Card className="flex-1">
                <CardContent className="p-6">
                    <div className="flex flex-col items-center justify-center h-[200px]">
                        <p className="text-sm font-medium mb-2">Analyzing File {slot}...</p>
                        <div className="w-full max-w-xs space-y-2">
                            <div className="flex items-center justify-between text-xs text-muted-foreground">
                                <span>{fileSlot.progress.message}</span>
                                <span className="tabular-nums">{fileSlot.progress.percent}%</span>
                            </div>
                            <Progress value={fileSlot.progress.percent} className="h-2" />
                        </div>
                        <p className="text-xs text-muted-foreground mt-2 truncate max-w-full">{fileNameFromPath(fileSlot.filePath)}</p>
                    </div>
                </CardContent>
            </Card>
        );
    }

    if (fileSlot.result) {
        const r = fileSlot.result;
        return (
            <Card className="flex-1">
                <CardHeader className="pb-2">
                    <CardTitle className="text-sm truncate">File {slot}: {fileNameFromPath(fileSlot.filePath)}</CardTitle>
                </CardHeader>
                <CardContent className="p-4 pt-0">
                    <div className="grid grid-cols-2 gap-x-4 gap-y-1 text-xs">
                        <span className="text-muted-foreground">Format</span><span>{r.file_type}</span>
                        <span className="text-muted-foreground">Sample Rate</span><span>{r.sample_rate >= 1000 ? `${(r.sample_rate / 1000).toFixed(1)} kHz` : `${r.sample_rate} Hz`}</span>
                        <span className="text-muted-foreground">Bit Depth</span><span>{r.bit_depth}</span>
                        <span className="text-muted-foreground">Channels</span><span>{r.channels}</span>
                        <span className="text-muted-foreground">Dynamic Range</span><span>{r.dynamic_range.toFixed(2)} dB</span>
                        <span className="text-muted-foreground">Peak</span><span>{r.peak_amplitude.toFixed(2)} dB</span>
                        <span className="text-muted-foreground">RMS</span><span>{r.rms_level.toFixed(2)} dB</span>
                    </div>
                    <Button variant="ghost" size="sm" className="mt-2 w-full text-xs" onClick={handleSelectFile}>
                        Replace File
                    </Button>
                </CardContent>
            </Card>
        );
    }

    return (
        <Card className="flex-1">
            <CardContent className="p-0">
                <input ref={fileInputRef} type="file" accept={SUPPORTED_AUDIO_ACCEPT} className="hidden" onChange={handleInputChange} />
                <div
                    className={`flex flex-col items-center justify-center h-[200px] border-2 border-dashed rounded-lg m-2 transition-colors cursor-pointer ${isDragging ? "border-primary bg-primary/10" : "border-muted-foreground/30"}`}
                    onDragOver={(e) => { e.preventDefault(); setIsDragging(true); }}
                    onDragLeave={(e) => { e.preventDefault(); setIsDragging(false); }}
                    onDrop={handleHtmlDrop}
                    onClick={handleSelectFile}
                    style={{ "--wails-drop-target": "drop" } as CSSProperties}
                >
                    <Upload className="h-8 w-8 text-muted-foreground mb-2" />
                    <p className="text-sm font-medium">File {slot}</p>
                    <p className="text-xs text-muted-foreground mt-1">
                        {isDragging ? "Drop here" : "Click or drag an audio file"}
                    </p>
                </div>
            </CardContent>
        </Card>
    );
}

export function AudioComparePage() {
    const {
        slotA, slotB, comparisons, aiVerdict, aiLoading,
        analyzeSlot, analyzeSlotPath, requestAIComparison, clearAll,
    } = useAudioCompare();

    const activeSlotRef = useRef<"A" | "B">("A");

    useEffect(() => {
        OnFileDrop((_x, _y, paths) => {
            const droppedPath = paths?.[0];
            if (!droppedPath) return;
            if (!isSupportedAudioPath(droppedPath)) {
                toast.error("Invalid File Type", { description: "Please select a FLAC, MP3, M4A, or AAC file" });
                return;
            }
            // Assign to slot A if empty, else slot B if empty, else replace the active slot
            const targetSlot = !slotA.result && !slotA.analyzing ? "A"
                : !slotB.result && !slotB.analyzing ? "B"
                : activeSlotRef.current;
            analyzeSlotPath(targetSlot, droppedPath);
        }, true);
        return () => { OnFileDropOff(); };
    }, [slotA.result, slotA.analyzing, slotB.result, slotB.analyzing, analyzeSlotPath]);

    const hasResults = !!(slotA.result && slotB.result);
    const isAnalyzing = slotA.analyzing || slotB.analyzing;

    return (
        <div className="space-y-4">
            <div className="flex items-center justify-between">
                <h2 className="text-lg font-semibold">Compare Audio Files</h2>
                {(slotA.result || slotB.result) && (
                    <Button variant="outline" size="sm" onClick={clearAll}>
                        <Trash2 className="h-4 w-4 mr-1" />
                        Clear All
                    </Button>
                )}
            </div>

            {/* Two file drop zones */}
            <div className="grid grid-cols-2 gap-4">
                <FileDropZone slot="A" fileSlot={slotA} onFile={analyzeSlot} onPath={analyzeSlotPath} />
                <FileDropZone slot="B" fileSlot={slotB} onFile={analyzeSlot} onPath={analyzeSlotPath} />
            </div>

            {/* Comparison table */}
            {comparisons && (
                <Card>
                    <CardHeader className="pb-2">
                        <CardTitle className="text-sm">Comparison Results</CardTitle>
                    </CardHeader>
                    <CardContent className="p-0">
                        <div className="overflow-x-auto">
                            <table className="w-full text-sm">
                                <thead>
                                    <tr className="border-b">
                                        <th className="text-left p-3 text-muted-foreground font-medium">Metric</th>
                                        <th className="text-left p-3 text-muted-foreground font-medium">File A</th>
                                        <th className="text-left p-3 text-muted-foreground font-medium">File B</th>
                                        <th className="text-left p-3 text-muted-foreground font-medium">Result</th>
                                    </tr>
                                </thead>
                                <tbody>
                                    {comparisons.map((c) => (
                                        <tr key={c.label} className="border-b last:border-0">
                                            <td className="p-3 font-medium">{c.label}</td>
                                            <td className={`p-3 ${c.winner === "A" ? "text-green-500 font-semibold" : c.winner === "B" ? "text-red-400" : ""}`}>
                                                {c.valueA} <WinnerBadge winner={c.winner} side="A" />
                                            </td>
                                            <td className={`p-3 ${c.winner === "B" ? "text-green-500 font-semibold" : c.winner === "A" ? "text-red-400" : ""}`}>
                                                {c.valueB} <WinnerBadge winner={c.winner} side="B" />
                                            </td>
                                            <td className="p-3 text-xs text-muted-foreground">{c.explanation}</td>
                                        </tr>
                                    ))}
                                </tbody>
                            </table>
                        </div>
                    </CardContent>
                </Card>
            )}

            {/* AI Verdict */}
            {hasResults && (
                <Card>
                    <CardHeader className="pb-2">
                        <CardTitle className="text-sm">AI Opinion</CardTitle>
                    </CardHeader>
                    <CardContent className="space-y-3">
                        {aiVerdict ? (
                            <p className="text-sm leading-relaxed whitespace-pre-wrap">{aiVerdict}</p>
                        ) : (
                            <p className="text-sm text-muted-foreground">
                                Click the button below to get an AI-powered comparison verdict.
                            </p>
                        )}
                        <Button
                            onClick={requestAIComparison}
                            disabled={aiLoading || isAnalyzing}
                            size="sm"
                        >
                            {aiLoading ? (
                                <>
                                    <Loader2 className="h-4 w-4 mr-1 animate-spin" />
                                    Analyzing...
                                </>
                            ) : (
                                <>
                                    <Sparkles className="h-4 w-4 mr-1" />
                                    {aiVerdict ? "Regenerate AI Opinion" : "Get AI Opinion"}
                                </>
                            )}
                        </Button>
                    </CardContent>
                </Card>
            )}

            {/* Spectrograms side by side */}
            {hasResults && slotA.result?.spectrum && slotB.result?.spectrum && (
                <div className="grid grid-cols-2 gap-4">
                    <div>
                        <p className="text-sm font-medium mb-2">File A: {fileNameFromPath(slotA.filePath)}</p>
                        <SpectrumVisualization
                            sampleRate={slotA.result!.sample_rate}
                            duration={slotA.result!.duration}
                            spectrumData={slotA.result!.spectrum}
                            fileName={fileNameFromPath(slotA.filePath)}
                        />
                    </div>
                    <div>
                        <p className="text-sm font-medium mb-2">File B: {fileNameFromPath(slotB.filePath)}</p>
                        <SpectrumVisualization
                            sampleRate={slotB.result!.sample_rate}
                            duration={slotB.result!.duration}
                            spectrumData={slotB.result!.spectrum}
                            fileName={fileNameFromPath(slotB.filePath)}
                        />
                    </div>
                </div>
            )}
        </div>
    );
}
