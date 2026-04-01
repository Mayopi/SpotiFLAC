import { useState, useEffect, useRef } from "react";
import { Trash2, Copy, Check, FileDown, ChevronRight, ChevronDown } from "lucide-react";
import { Button } from "@/components/ui/button";
import { logger, type LogEntry } from "@/lib/logger";
import { useDownloadQueueData } from "@/hooks/useDownloadQueueData";
import { ExportFailedDownloads } from "../../wailsjs/go/main/App";
import { toastWithSound as toast } from "@/lib/toast-with-sound";
const levelColors: Record<string, string> = {
    info: "text-blue-500",
    success: "text-green-500",
    warning: "text-yellow-500",
    error: "text-red-500",
    debug: "text-gray-500",
};
function formatTime(date: Date): string {
    return date.toLocaleTimeString("en-US", {
        hour12: false,
        hour: "2-digit",
        minute: "2-digit",
        second: "2-digit",
    });
}

function isCodeLike(message: string): boolean {
    return /HTTP|status\s*[:\d]|[\{}\[\]]|https?:\/\/\S+[?&=]/.test(message);
}

function LogEntryRow({ log }: { log: LogEntry }) {
    const [expanded, setExpanded] = useState(false);
    const message = log.message;
    const long = message.length > 120;
    const codeLike = isCodeLike(message);
    const needsExpand = long || codeLike;

    if (!needsExpand) {
        return (
            <div className="flex gap-2 py-0.5">
                <span className="text-muted-foreground shrink-0">
                    [{formatTime(log.timestamp)}]
                </span>
                <span className={`shrink-0 w-16 ${levelColors[log.level]}`}>
                    [{log.level}]
                </span>
                <span className="break-all">{message}</span>
            </div>
        );
    }

    const preview = long ? message.slice(0, 120) + "..." : message;

    return (
        <div className="py-0.5">
            <div
                className="flex gap-2 cursor-pointer select-none hover:bg-muted/50 rounded"
                onClick={() => setExpanded((prev) => !prev)}
            >
                <span className="text-muted-foreground shrink-0">
                    [{formatTime(log.timestamp)}]
                </span>
                <span className={`shrink-0 w-16 ${levelColors[log.level]}`}>
                    [{log.level}]
                </span>
                <span className="shrink-0 text-muted-foreground">
                    {expanded ? <ChevronDown className="h-3 w-3 inline" /> : <ChevronRight className="h-3 w-3 inline" />}
                </span>
                {!expanded && <span className="break-all text-muted-foreground">{preview}</span>}
            </div>
            {expanded && (
                <pre className="bg-muted rounded px-2 py-1 mt-1 ml-[calc(theme(spacing.16)+theme(spacing.2))] whitespace-pre-wrap break-all text-[11px] leading-relaxed overflow-x-auto max-h-60 overflow-y-auto">
                    {message}
                </pre>
            )}
        </div>
    );
}

export function DebugLoggerPage() {
    const [logs, setLogs] = useState<LogEntry[]>([]);
    const [copied, setCopied] = useState(false);
    const scrollRef = useRef<HTMLDivElement>(null);
    const queueInfo = useDownloadQueueData();
    const hasDownloadActivity = queueInfo.queue.length > 0 ||
        queueInfo.queued_count > 0 ||
        queueInfo.completed_count > 0 ||
        queueInfo.failed_count > 0 ||
        queueInfo.skipped_count > 0;
    const canExportFailed = hasDownloadActivity && queueInfo.failed_count > 0;
    useEffect(() => {
        const unsubscribe = logger.subscribe(() => {
            setLogs(logger.getLogs());
        });
        setLogs(logger.getLogs());
        return () => {
            unsubscribe();
        };
    }, []);
    useEffect(() => {
        if (scrollRef.current) {
            scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
        }
    }, [logs]);
    const handleClear = () => {
        logger.clear();
    };
    const handleCopy = async () => {
        const logText = logs
            .map((log) => `[${formatTime(log.timestamp)}] [${log.level}] ${log.message}`)
            .join("\n");
        try {
            await navigator.clipboard.writeText(logText);
            setCopied(true);
            setTimeout(() => setCopied(false), 500);
        }
        catch (err) {
            console.error("Failed to copy logs:", err);
        }
    };
    const handleExportFailed = async () => {
        if (!canExportFailed) {
            return;
        }
        try {
            const message = await ExportFailedDownloads();
            if (message.startsWith("Successfully")) {
                toast.success(message);
            }
            else if (message !== "Export cancelled") {
                toast.info(message);
            }
        }
        catch (error) {
            console.error("Failed to export:", error);
            toast.error(`Failed to export: ${error}`);
        }
    };
    return (<div className="space-y-6">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-bold">Debug Logs</h1>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" className="gap-1.5" onClick={handleExportFailed} disabled={!canExportFailed}>
            <FileDown className="h-4 w-4"/>
            Export Failed
          </Button>
          <Button variant="outline" size="sm" className="gap-1.5" onClick={handleCopy} disabled={logs.length === 0}>
            {copied ? <Check className="h-4 w-4"/> : <Copy className="h-4 w-4"/>}
            Copy
          </Button>
          <Button variant="outline" size="sm" className="gap-1.5" onClick={handleClear} disabled={logs.length === 0}>
            <Trash2 className="h-4 w-4"/>
            Clear
          </Button>
        </div>
      </div>

      <div ref={scrollRef} className="h-[calc(100vh-220px)] overflow-y-auto bg-muted/50 rounded-lg p-4 font-mono text-xs">
        {logs.length === 0 ? (<p className="text-muted-foreground lowercase">no logs yet...</p>) : (logs.map((log, i) => (
            <LogEntryRow key={i} log={log} />
        )))}
      </div>
    </div>);
}
