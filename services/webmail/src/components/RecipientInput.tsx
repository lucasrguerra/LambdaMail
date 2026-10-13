"use client";

import React, { useRef, useState } from "react";
import { X } from "lucide-react";
import { useTranslations } from "../i18n/provider";
import { dedupe, isValidAddress, parseAddress, parseAddressList } from "../lib/recipients";

interface Props {
  id: string;
  /** Entries as they will be sent: `Name <addr>` or a bare address. */
  value: string[];
  onChange: (next: string[]) => void;
  placeholder?: string;
  disabled?: boolean;
  /** Outlines the field when something in it cannot be sent. */
  invalid?: boolean;
}

/**
 * An address field that turns each recipient into a chip.
 *
 * Commit points are the ones people expect from a mail client: a comma, a
 * semicolon, Enter, a space after something that is already an address, and
 * leaving the field. A pasted list becomes several chips at once. A chip that
 * is not an address stays visible and red rather than being dropped, so a
 * typo is something the sender sees instead of mail that quietly goes nowhere.
 */
export function RecipientInput({ id, value, onChange, placeholder, disabled, invalid }: Props) {
  const t = useTranslations();
  const [draft, setDraft] = useState("");
  const inputRef = useRef<HTMLInputElement>(null);

  const commit = (text: string) => {
    const parsed = parseAddressList(text);
    if (parsed.length === 0) return;
    onChange(dedupe([...value, ...parsed.map((p) => p.entry)]));
  };

  const flush = () => {
    if (draft.trim()) commit(draft);
    setDraft("");
  };

  const remove = (index: number) => {
    onChange(value.filter((_, i) => i !== index));
    inputRef.current?.focus();
  };

  const handleChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const text = e.target.value;
    // Only a separator that is not inside a quoted name or an <address> ends a
    // recipient; parseAddressList knows the difference, so it is asked rather
    // than splitting here.
    if (/[,;]$/.test(text)) {
      const body = text.slice(0, -1);
      const unbalanced = (body.match(/"/g)?.length ?? 0) % 2 === 1 || (body.includes("<") && !body.includes(">"));
      if (!unbalanced) {
        commit(body);
        setDraft("");
        return;
      }
    }
    setDraft(text);
  };

  const handleKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "Enter") {
      // Enter in this field must never submit the message.
      e.preventDefault();
      flush();
    } else if (e.key === " " && isValidAddress(draft.trim())) {
      e.preventDefault();
      flush();
    } else if (e.key === "Backspace" && draft === "" && value.length > 0) {
      onChange(value.slice(0, -1));
    }
  };

  const handlePaste = (e: React.ClipboardEvent<HTMLInputElement>) => {
    const text = e.clipboardData.getData("text");
    // A single-line input swallows line breaks on paste, so a column of
    // addresses from a spreadsheet would arrive glued together.
    if (/[\n\r,;\s]/.test(text.trim())) {
      e.preventDefault();
      commit(draft + text);
      setDraft("");
    }
  };

  return (
    <div
      // Clicking anywhere in the row focuses the input, like a text field.
      onClick={() => inputRef.current?.focus()}
      className={`flex min-h-[34px] min-w-[160px] flex-1 flex-wrap items-center gap-1.5 rounded-lg px-1 py-0.5 ${
        invalid ? "ring-1 ring-rose-500/70" : ""
      }`}
    >
      {value.map((entry, index) => {
        const parsed = parseAddress(entry);
        return (
          <span
            key={`${entry}-${index}`}
            title={parsed.valid ? parsed.address : t("mail.invalidAddress")}
            className={`inline-flex max-w-full items-center gap-1 rounded-full py-0.5 pl-2.5 pr-1 text-[12.5px] ${
              parsed.valid
                ? "bg-indigo-900/70 text-indigo-100 shadow-[inset_0_0_0_1px_#5d5294]"
                : "bg-rose-900/60 text-rose-100 shadow-[inset_0_0_0_1px_rgba(244,63,94,0.5)]"
            }`}
          >
            <span className="truncate">{parsed.label}</span>
            {!disabled && (
              <button
                type="button"
                onClick={(e) => {
                  e.stopPropagation();
                  remove(index);
                }}
                aria-label={t("mail.removeRecipient", { name: parsed.label })}
                className="flex h-5 w-5 flex-none items-center justify-center rounded-full hover:bg-white/15 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-indigo-300"
              >
                <X className="h-3 w-3" />
              </button>
            )}
          </span>
        );
      })}
      <input
        ref={inputRef}
        id={id}
        type="text"
        inputMode="email"
        autoComplete="off"
        value={draft}
        onChange={handleChange}
        onKeyDown={handleKeyDown}
        onPaste={handlePaste}
        onBlur={flush}
        placeholder={value.length === 0 ? placeholder : undefined}
        disabled={disabled}
        aria-invalid={invalid || undefined}
        className="min-h-[28px] min-w-[140px] flex-1 border-0 bg-transparent px-1 text-sm text-slate-100 placeholder-slate-500 focus:outline-none"
      />
    </div>
  );
}
