// SPDX-License-Identifier: AGPL-3.0-or-later

import { CaretDown, CaretUp, CaretUpDown } from "@phosphor-icons/react";
import { Fragment, type ReactNode } from "react";
import { compare } from "../format";

export type SortDir = "asc" | "desc";
export type Sort = { key: string; dir: SortDir };

export type Column<T> = {
  key: string;
  header: string;
  cell: (row: T) => ReactNode;
  /** The value to sort by; columns without it are not sortable. */
  sortValue?: (row: T) => string | number;
  /** Hides the header text visually, for a column of row actions. */
  srHeader?: boolean;
  /** Replaces the header text with a control, for example a "select all"
   *  checkbox; header stays its accessible name. */
  headerCell?: ReactNode;
  /** The title of a row on a phone card, shown without a label. */
  main?: boolean;
};

type TableProps<T> = {
  /** Accessible name, for example "Sites". */
  label: string;
  columns: readonly Column<T>[];
  rows: readonly T[];
  rowKey: (row: T) => string;
  sort?: Sort | undefined;
  onSort?: (sort: Sort) => void;
  /** Shown in place of the rows when there are none. */
  empty?: ReactNode;
  /**
   * One line per row on a phone: the main column and the row actions; the
   * labelled columns are hidden, so the main cell should carry them.
   */
  compact?: boolean;
  /** Content of an extra full-width row below a row, for example a
   *  confirmation for that row; null for none. */
  after?: (row: T) => ReactNode;
};


/** Sorts rows by a column without changing the input. Stable. */
export function sortRows<T>(rows: readonly T[], columns: readonly Column<T>[], sort: Sort | undefined): T[] {
  const col = sort && columns.find((c) => c.key === sort.key);
  if (!sort || !col?.sortValue) return [...rows];
  const value = col.sortValue;
  const sign = sort.dir === "asc" ? 1 : -1;
  return rows
    .map((row, i) => ({ row, i, v: value(row) }))
    .sort((a, b) => {
      const c = typeof a.v === "number" && typeof b.v === "number" ? a.v - b.v : compare(String(a.v), String(b.v));
      return c !== 0 ? c * sign : a.i - b.i;
    })
    .map((x) => x.row);
}

/** The next sort after a click on a column header: asc, then desc. */
export function nextSort(current: Sort | undefined, key: string): Sort {
  if (current?.key === key && current.dir === "asc") return { key, dir: "desc" };
  return { key, dir: "asc" };
}

/**
 * A data table. Sortable columns get a button in their header with a neutral
 * up/down icon, and an up or down arrow on the active column (ADR-0037).
 * The caller keeps the sort, for example in the address.
 */
export function Table<T>({ label, columns, rows, rowKey, sort, onSort, empty, compact = false, after }: TableProps<T>) {
  const shown = sortRows(rows, columns, sort);
  return (
    <div className="ui-table-box">
      <table className="ui-table" aria-label={label} data-compact={compact ? "" : undefined}>
        <thead>
          <tr>
            {columns.map((c) => {
              const active = sort?.key === c.key;
              const ariaSort = active ? (sort.dir === "asc" ? "ascending" : "descending") : undefined;
              const sortable = c.sortValue !== undefined && onSort !== undefined;
              const Icon = !active ? CaretUpDown : sort.dir === "asc" ? CaretUp : CaretDown;
              return (
                <th key={c.key} scope="col" aria-sort={ariaSort}>
                  {c.headerCell !== undefined ? (
                    c.headerCell
                  ) : c.srHeader ? (
                    <span className="ui-sr-only">{c.header}</span>
                  ) : sortable ? (
                    <button
                      type="button"
                      className="ui-sort"
                      data-active={active}
                      onClick={() => onSort(nextSort(sort, c.key))}
                    >
                      {c.header}
                      <Icon size={14} aria-hidden="true" />
                    </button>
                  ) : (
                    c.header
                  )}
                </th>
              );
            })}
          </tr>
        </thead>
        <tbody>
          {shown.length === 0 && empty !== undefined ? (
            <tr>
              <td className="ui-empty" colSpan={columns.length}>
                {empty}
              </td>
            </tr>
          ) : (
            shown.map((row) => {
              const extra = after?.(row) ?? null;
              return (
                <Fragment key={rowKey(row)}>
                  <tr data-open={extra !== null ? "" : undefined}>
                    {columns.map((c, i) => (
                      <td key={c.key} data-label={i === 0 || c.srHeader || c.main ? undefined : c.header} data-actions={c.srHeader ? "" : undefined} data-main={c.main ? "" : undefined}>
                        {c.cell(row)}
                      </td>
                    ))}
                  </tr>
                  {extra !== null && (
                    <tr className="ui-table-after">
                      <td colSpan={columns.length}>{extra}</td>
                    </tr>
                  )}
                </Fragment>
              );
            })
          )}
        </tbody>
      </table>
    </div>
  );
}
