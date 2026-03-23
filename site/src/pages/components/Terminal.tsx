import React, { type ReactNode } from "react";
import styles from "./Terminal.module.css";

interface Props {
  children: string;
  caption?: string;
}

export default function Terminal({ children, caption }: Props): ReactNode {
  const text = typeof children === "string" ? children : String(children ?? "");
  const lines = text.trim().split("\n");

  return (
    <figure className={styles.figure}>
      <pre className={styles.pre}>
        {lines.map((line, i) => (
          <div key={i}>{styleLine(line)}</div>
        ))}
      </pre>
      {caption && <figcaption>{caption}</figcaption>}
    </figure>
  );
}

function styleLine(line: string): ReactNode {
  if (!line.trim()) return "\u00a0";

  const trimmed = line.trimStart();
  const indent = line.slice(0, line.length - trimmed.length);

  if (trimmed.startsWith("$ ")) {
    return (
      <>
        {indent}
        <span className={styles.prompt}>$</span>{" "}
        <span className={styles.cmd}>{trimmed.slice(2)}</span>
      </>
    );
  }

  if (trimmed.includes("✓")) {
    return <span className={styles.ok}>{line}</span>;
  }

  if (trimmed.startsWith("! ") || trimmed.startsWith("l.")) {
    return <span className={styles.err}>{line}</span>;
  }

  if (trimmed === "█") {
    return (
      <>
        {indent}
        <span className={styles.prompt}>$</span>{" "}
        <span className={styles.cursor} />
      </>
    );
  }

  return <span className={styles.dim}>{line}</span>;
}
