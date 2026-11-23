// SPDX-License-Identifier: AGPL-3.0-or-later

import { X } from "@phosphor-icons/react";
import { useEffect, useState, type ReactNode } from "react";
import { Card } from "./Card";
import { IconButton } from "./Button";

export type JobState = "idle" | "running" | "succeeded" | "failed";

/**
 * useJobCard decides whether the card of a background job (install or
 * remove a runtime) shows. The server keeps the last job with its log, so
 * a finished job shows only when this page saw it run, and only until it
 * is closed. A reload starts clean.
 */
export function useJobCard(state: JobState | undefined): { show: boolean; close: () => void } {
  const [seen, setSeen] = useState(false);
  const [closed, setClosed] = useState(false);
  const running = state === "running";
  useEffect(() => {
    if (running) {
      setSeen(true);
      setClosed(false);
    }
  }, [running]);
  const finished = state === "succeeded" || state === "failed";
  return { show: running || (seen && finished && !closed), close: () => setClosed(true) };
}

type JobCardProps = {
  title: string;
  state: JobState;
  log: string[];
  /** Label of the log block, e.g. "Installation log". */
  logLabel: string;
  onClose: () => void;
  /** Shown above the log, e.g. the reason of a failure. */
  children?: ReactNode;
};

/**
 * The card of a running or just finished background job. The log stays
 * folded away unless the job failed; the card closes once the job ended.
 */
export function JobCard({ title, state, log, logLabel, onClose, children }: JobCardProps) {
  const running = state === "running";
  return (
    <Card title={title} actions={!running && <IconButton label="Close" icon={<X size={18} />} onClick={onClose} />}>
      <div className="ui-stack">
        {children}
        {log.length > 0 && (
          <details className="ui-job-log" open={state === "failed"}>
            <summary>{running ? "Show progress" : "Show log"}</summary>
            <pre className="ui-code" aria-label={logLabel}>
              {log.join("\n")}
            </pre>
          </details>
        )}
      </div>
    </Card>
  );
}
