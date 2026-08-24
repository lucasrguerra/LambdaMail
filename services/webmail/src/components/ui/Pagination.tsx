"use client";

import React from "react";
import { ChevronLeft, ChevronRight } from "lucide-react";
import { Button } from "./Button";
import { pageWindow, pageRange } from "../../lib/paging";
import { useTranslations } from "../../i18n/provider";

interface PaginationProps {
  page: number;
  pageSize: number;
  total: number;
  totalPages: number;
  onPage: (page: number) => void;
  busy?: boolean;
}

/**
 * The pager under a server-paged list.
 *
 * The lists it sits under fetch one page at a time, so every control here
 * moves the page the server is asked for - nothing is sliced in the browser.
 */
export function Pagination({ page, pageSize, total, totalPages, onPage, busy }: PaginationProps) {
  const t = useTranslations();
  const { from, to } = pageRange(page, pageSize, total);

  // One page of results needs no controls, but the count is still worth
  // stating so an empty list is distinguishable from a broken one.
  const showControls = totalPages > 1;

  return (
    <div className="flex flex-wrap items-center justify-between gap-3 pt-1">
      <span className="text-[12px] text-slate-400">
        {total > 0 ? t("ui.showingRange", { from, to, total }) : t("ui.noResults")}
      </span>

      {showControls && (
        <div className="flex flex-wrap items-center gap-1.5">
          <Button
            variant="secondary"
            size="sm"
            onClick={() => onPage(page - 1)}
            disabled={busy || page <= 1}
            aria-label={t("ui.previousPage")}
          >
            <ChevronLeft className="h-3.5 w-3.5" />
          </Button>

          {pageWindow(page, totalPages).map((p) => (
            <Button
              key={p}
              variant={p === page ? "primary" : "secondary"}
              size="sm"
              onClick={() => onPage(p)}
              disabled={busy}
              aria-current={p === page ? "page" : undefined}
            >
              <span className="tabular-nums">{p}</span>
            </Button>
          ))}

          <Button
            variant="secondary"
            size="sm"
            onClick={() => onPage(page + 1)}
            disabled={busy || page >= totalPages}
            aria-label={t("ui.nextPage")}
          >
            <ChevronRight className="h-3.5 w-3.5" />
          </Button>
        </div>
      )}
    </div>
  );
}
