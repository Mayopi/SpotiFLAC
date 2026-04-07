import type { AnalysisResult } from "@/types/api";

export interface MetricComparison {
    label: string;
    valueA: string;
    valueB: string;
    winner: "A" | "B" | "tie" | "na";
    explanation: string;
}

const FORMAT_RANK: Record<string, number> = {
    FLAC: 3,
    M4A: 2,
    AAC: 2,
    MP3: 1,
};

function formatFileSize(bytes: number): string {
    if (bytes < 1024) return `${bytes} B`;
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
    return `${(bytes / (1024 * 1024)).toFixed(2)} MB`;
}

function formatDuration(seconds: number): string {
    const m = Math.floor(seconds / 60);
    const s = (seconds % 60).toFixed(1);
    return `${m}:${s.padStart(4, "0")}`;
}

function formatHz(hz: number): string {
    if (hz >= 1000) return `${(hz / 1000).toFixed(1)} kHz`;
    return `${hz} Hz`;
}

function higherIsBetter(a: number, b: number, labelA: string, labelB: string): Pick<MetricComparison, "winner" | "explanation"> {
    if (a === b) return { winner: "tie", explanation: "Both are equal" };
    if (a > b) return { winner: "A", explanation: `${labelA} is higher` };
    return { winner: "B", explanation: `${labelB} is higher` };
}

export function compareResults(a: AnalysisResult, b: AnalysisResult): MetricComparison[] {
    const nameA = a.file_path.split(/[/\\]/).pop() || "File A";
    const nameB = b.file_path.split(/[/\\]/).pop() || "File B";
    const comparisons: MetricComparison[] = [];

    // Format
    const formatA = a.file_type || "Unknown";
    const formatB = b.file_type || "Unknown";
    const rankA = FORMAT_RANK[formatA] ?? 0;
    const rankB = FORMAT_RANK[formatB] ?? 0;
    comparisons.push({
        label: "Format",
        valueA: formatA,
        valueB: formatB,
        winner: rankA === rankB ? "tie" : rankA > rankB ? "A" : "B",
        explanation: rankA === rankB ? "Same format tier" : rankA > rankB ? `${formatA} is lossless/higher tier` : `${formatB} is lossless/higher tier`,
    });

    // Sample Rate
    comparisons.push({
        label: "Sample Rate",
        valueA: formatHz(a.sample_rate),
        valueB: formatHz(b.sample_rate),
        ...higherIsBetter(a.sample_rate, b.sample_rate, nameA, nameB),
    });

    // Bit Depth
    comparisons.push({
        label: "Bit Depth",
        valueA: a.bit_depth,
        valueB: b.bit_depth,
        ...higherIsBetter(a.bits_per_sample, b.bits_per_sample, nameA, nameB),
    });

    // Channels
    comparisons.push({
        label: "Channels",
        valueA: String(a.channels),
        valueB: String(b.channels),
        ...higherIsBetter(a.channels, b.channels, nameA, nameB),
    });

    // Dynamic Range (higher is better)
    comparisons.push({
        label: "Dynamic Range",
        valueA: `${a.dynamic_range.toFixed(2)} dB`,
        valueB: `${b.dynamic_range.toFixed(2)} dB`,
        ...higherIsBetter(a.dynamic_range, b.dynamic_range, nameA, nameB),
    });

    // Peak Amplitude (closer to 0 dB is louder but not necessarily better; treat as informational)
    comparisons.push({
        label: "Peak Amplitude",
        valueA: `${a.peak_amplitude.toFixed(2)} dB`,
        valueB: `${b.peak_amplitude.toFixed(2)} dB`,
        winner: "na",
        explanation: "Informational — depends on mastering intent",
    });

    // RMS Level
    comparisons.push({
        label: "RMS Level",
        valueA: `${a.rms_level.toFixed(2)} dB`,
        valueB: `${b.rms_level.toFixed(2)} dB`,
        winner: "na",
        explanation: "Informational — depends on mastering intent",
    });

    // Duration
    comparisons.push({
        label: "Duration",
        valueA: formatDuration(a.duration),
        valueB: formatDuration(b.duration),
        winner: "na",
        explanation: "Informational",
    });

    // File Size
    comparisons.push({
        label: "File Size",
        valueA: formatFileSize(a.file_size),
        valueB: formatFileSize(b.file_size),
        winner: "na",
        explanation: "Informational",
    });

    // Bitrate
    const bitrateA = a.bitrate_kbps ?? (a.file_size * 8 / a.duration / 1000);
    const bitrateB = b.bitrate_kbps ?? (b.file_size * 8 / b.duration / 1000);
    comparisons.push({
        label: "Bitrate",
        valueA: `${Math.round(bitrateA)} kbps`,
        valueB: `${Math.round(bitrateB)} kbps`,
        ...higherIsBetter(bitrateA, bitrateB, nameA, nameB),
    });

    return comparisons;
}

export function buildComparisonPrompt(a: AnalysisResult, b: AnalysisResult): string {
    const nameA = a.file_path.split(/[/\\]/).pop() || "File A";
    const nameB = b.file_path.split(/[/\\]/).pop() || "File B";

    return `Compare these two audio files:

**File A: ${nameA}**
- Format: ${a.file_type || "Unknown"}
- Sample Rate: ${formatHz(a.sample_rate)}
- Bit Depth: ${a.bit_depth}
- Channels: ${a.channels}
- Dynamic Range: ${a.dynamic_range.toFixed(2)} dB
- Peak Amplitude: ${a.peak_amplitude.toFixed(2)} dB
- RMS Level: ${a.rms_level.toFixed(2)} dB
- Duration: ${formatDuration(a.duration)}
- File Size: ${formatFileSize(a.file_size)}
- Bitrate: ${Math.round(a.bitrate_kbps ?? (a.file_size * 8 / a.duration / 1000))} kbps
${a.codec_mode ? `- Codec Mode: ${a.codec_mode}` : ""}

**File B: ${nameB}**
- Format: ${b.file_type || "Unknown"}
- Sample Rate: ${formatHz(b.sample_rate)}
- Bit Depth: ${b.bit_depth}
- Channels: ${b.channels}
- Dynamic Range: ${b.dynamic_range.toFixed(2)} dB
- Peak Amplitude: ${b.peak_amplitude.toFixed(2)} dB
- RMS Level: ${b.rms_level.toFixed(2)} dB
- Duration: ${formatDuration(b.duration)}
- File Size: ${formatFileSize(b.file_size)}
- Bitrate: ${Math.round(b.bitrate_kbps ?? (b.file_size * 8 / b.duration / 1000))} kbps
${b.codec_mode ? `- Codec Mode: ${b.codec_mode}` : ""}

Which file has better audio quality? Provide a concise technical verdict.`;
}
