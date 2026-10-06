// Code generated from schemas/catalog.json by scripts/catalog-types.mjs. DO NOT EDIT.

/**
 * How a value stands: ok, warn, error or muted.
 *
 * This interface was referenced by `Catalog`'s JSON-Schema
 * via the `definition` "tone".
 */
export type Tone = "ok" | "warn" | "error" | "muted";
/**
 * This interface was referenced by `Catalog`'s JSON-Schema
 * via the `definition` "ActionsValue".
 */
export type ActionsValue = Action[];
/**
 * This interface was referenced by `Catalog`'s JSON-Schema
 * via the `definition` "StatValue".
 */
export type StatValue = number | string | boolean | null;

export interface Catalog {
  tone?: Tone;
  action?: Action;
  topic?: Topic;
  reply?: Reply;
  data?: Data;
  ActionsValue?: ActionsValue;
  CardValue?: CardValue;
  StatValue?: StatValue;
  ChartValue?: ChartValue;
  BoardValue?: BoardValue;
  FormValue?: FormValue;
  DocumentValue?: DocumentValue;
  CustomValue?: CustomValue;
  ChatValue?: ChatValue;
}
/**
 * A button: a command call or a move to a screen. verb names a built-in label the client translates.
 *
 * This interface was referenced by `Catalog`'s JSON-Schema
 * via the `definition` "action".
 */
export interface Action {
  label?: string;
  verb?: string;
  /**
   * Name of a Tabler icon on the button.
   */
  icon?: string;
  primary?: boolean;
  command?: string;
  params?: {};
  /**
   * Param the owner types before the call.
   */
  input?: string;
  /**
   * Time after which the button is hidden.
   */
  until?: string;
  project?: string;
  screen?: string;
  record?: number;
  /**
   * Class of the command of a custom block's button; the host asks the owner before one that is not internal.
   */
  class?: string;
  /**
   * The client asks the owner before the call: a button of deck that deletes or revokes.
   */
  confirm?: boolean;
  /**
   * Values the owner picks the input from.
   */
  options?: string[];
  /**
   * The result names a chat topic, which the client opens.
   */
  chat?: boolean;
  /**
   * Stored file the button downloads instead of a call.
   */
  file?: number;
  undo?: Action1;
}
/**
 * The call that takes this one back; the client offers it for a moment after the call went through.
 */
export interface Action1 {
  label?: string;
  verb?: string;
  /**
   * Name of a Tabler icon on the button.
   */
  icon?: string;
  primary?: boolean;
  command?: string;
  params?: {};
  /**
   * Param the owner types before the call.
   */
  input?: string;
  /**
   * Time after which the button is hidden.
   */
  until?: string;
  project?: string;
  screen?: string;
  record?: number;
  /**
   * Class of the command of a custom block's button; the host asks the owner before one that is not internal.
   */
  class?: string;
  /**
   * The client asks the owner before the call: a button of deck that deletes or revokes.
   */
  confirm?: boolean;
  /**
   * Values the owner picks the input from.
   */
  options?: string[];
  /**
   * The result names a chat topic, which the client opens.
   */
  chat?: boolean;
  /**
   * Stored file the button downloads instead of a call.
   */
  file?: number;
  undo?: Action1;
}
/**
 * This interface was referenced by `Catalog`'s JSON-Schema
 * via the `definition` "topic".
 */
export interface Topic {
  id: number;
  title: string;
  card?: number;
  /**
   * the project of its card, else of the screen of its first message
   */
  project?: string;
  archived: boolean;
  updated_at: string;
  /**
   * the last message, cut short
   */
  last?: string;
  reply?: Reply;
}
/**
 * This interface was referenced by `Catalog`'s JSON-Schema
 * via the `definition` "reply".
 */
export interface Reply {
  process: number;
  state: string;
  provider?: string;
  error?: string;
  open?: Action;
}
/**
 * This interface was referenced by `Catalog`'s JSON-Schema
 * via the `definition` "data".
 */
export interface Data {
  state: "ready" | "empty" | "error";
  error?: string;
  /**
   * the text of an empty block
   */
  empty?: string;
  tone?: Tone;
  /**
   * Choices the owner narrows the block by; the client sends the picked ones back to deck.screen.
   */
  filters?: {
    field: string;
    /**
     * the field in the owner's language
     */
    label?: string;
    value?: string;
    options: string[];
  }[];
  rows?: {
    id: unknown;
    cells: unknown[];
    /**
     * the tone of each cell, null for none
     */
    tones?: (Tone | null)[];
    /**
     * the text of each cell shown in full on demand, null for none; such a cell shows its value in one line
     */
    details?: (string | null)[];
    actions?: Action[];
  }[];
  items?: {
    id: unknown;
    title: string;
    text?: string;
    meta?: string;
    icon?: string;
    /**
     * address of the picture of the line
     */
    image?: string;
    details?: string;
    /**
     * text is markdown
     */
    markdown?: boolean;
    tone?: Tone;
    questions?: {
      question: string;
      multiSelect?: boolean;
      options?: {
        label?: string;
        description?: string;
      }[];
    }[];
    actions?: Action[];
  }[];
  value?: unknown;
  files?: {
    id: unknown;
    title?: string;
    name?: string;
    type?: string;
    size?: number;
    /**
     * where the client reads the file, with its token
     */
    url?: string;
    error?: string;
    /**
     * the file as item.file gives it, which the mark of a player passes on
     */
    file?: {
      [k: string]: unknown;
    };
    marks?: {
      at: number;
      text?: string;
    }[];
    mark?: Action;
  }[];
  lines?: {
    at: string;
    kind: string;
    id?: string;
    title?: string;
    status?: string;
    text?: string;
  }[];
}
/**
 * This interface was referenced by `Catalog`'s JSON-Schema
 * via the `definition` "CardValue".
 */
export interface CardValue {
  fields: {
    label: string;
    value?: unknown;
    markdown?: boolean;
    format?: string;
    currency?: string;
    tone?: Tone;
  }[];
  actions: Action[];
}
/**
 * This interface was referenced by `Catalog`'s JSON-Schema
 * via the `definition` "ChartValue".
 */
export interface ChartValue {
  x: unknown[];
  series: {
    label: string;
    values: (number | null)[];
  }[];
}
/**
 * This interface was referenced by `Catalog`'s JSON-Schema
 * via the `definition` "BoardValue".
 */
export interface BoardValue {
  project: string;
  lanes: {
    name: string;
    title: string;
    role?: string;
    cards: {
      id: number;
      title: string;
      text?: string;
      waits?: number[];
      record?: number;
      actions?: Action[];
    }[];
  }[];
  deleted: {
    id: number;
    title: string;
  }[];
}
/**
 * This interface was referenced by `Catalog`'s JSON-Schema
 * via the `definition` "FormValue".
 */
export interface FormValue {
  project: string;
  commands: {
    name: string;
    title: string;
    description?: string;
    project?: string;
    input: {};
    order: string[] | null;
    result?: unknown;
  }[];
  values?: {};
  readonly?: string[];
  record?: number;
  save?: string;
  keep_open?: boolean;
  /**
   * Values offered by field
   */
  choices?: {
    [k: string]: {
      value: unknown;
      label: string;
    }[];
  };
}
/**
 * This interface was referenced by `Catalog`'s JSON-Schema
 * via the `definition` "DocumentValue".
 */
export interface DocumentValue {
  /**
   * 0 while the document is not written yet
   */
  id: number;
  project: string;
  name: string;
  kind: string;
  text: string;
  revision: number;
  editable: boolean;
  record?: number;
  card?: number;
  revisions: {
    revision: number;
    author: string;
    reason?: string;
    created_at: string;
  }[];
}
/**
 * This interface was referenced by `Catalog`'s JSON-Schema
 * via the `definition` "CustomValue".
 */
export interface CustomValue {
  html: string;
  items?: {}[];
  record?: {};
  actions: Action[];
}
/**
 * This interface was referenced by `Catalog`'s JSON-Schema
 * via the `definition` "ChatValue".
 */
export interface ChatValue {
  topic?: Topic;
  topics: Topic[];
  archived: Topic[];
  messages: {
    id: number;
    role: "owner" | "master";
    text: string;
    process?: number;
    files?: {
      id: number;
      name: string;
    }[];
    /**
     * the screen the owner wrote it on: project, screen, record, card, process
     */
    screen?: {};
    reply?: Reply;
    created_at: string;
  }[];
  questions: {
    ask: number;
    text: string;
    questions: {
      question: string;
      multiSelect?: boolean;
      options?: {
        label?: string;
        description?: string;
      }[];
    }[];
    answer: Action;
  }[];
  turn?: {
    process: number;
    state: string;
    provider?: string;
    /**
     * what the reply waits for: the owner, a slot, a provider
     */
    waits?: string;
    lines: {
      at: string;
      kind: string;
      id?: string;
      title?: string;
      status?: string;
      text?: string;
    }[];
    stop?: Action;
    open?: Action;
  };
  send: Action;
  archive?: Action;
  delete?: Action;
}
