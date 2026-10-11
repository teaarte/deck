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
   * With confirm, a read command the client calls with params and lists before the yes: what the call deletes for good, as kind and name; the yes waits until the list came.
   */
  parts?: string;
  /**
   * Values the owner picks the input from.
   */
  options?: string[];
  /**
   * With a command, the result names a chat topic, which the client opens; alone, the button opens the chat of the master in a new topic.
   */
  chat?: boolean;
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
   * With confirm, a read command the client calls with params and lists before the yes: what the call deletes for good, as kind and name; the yes waits until the list came.
   */
  parts?: string;
  /**
   * Values the owner picks the input from.
   */
  options?: string[];
  /**
   * With a command, the result names a chat topic, which the client opens; alone, the button opens the chat of the master in a new topic.
   */
  chat?: boolean;
  undo?: Action1;
}
/**
 * This interface was referenced by `Catalog`'s JSON-Schema
 * via the `definition` "topic".
 */
export interface Topic {
  id: number;
  title: string;
  /**
   * the project of the screen of its first message
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
  /**
   * the state in the owner's words
   */
  word?: string;
  tone?: Tone;
  /**
   * it waits for the owner or its provider rather than works
   */
  waits?: boolean;
  open?: Action;
  /**
   * what a reply that broke off left
   */
  cut?: {
    /**
     * the calls it made, in order
     */
    did: string[];
    undo: {
      text: string;
      action: Action;
    }[];
    /**
     * a button per provider it can go again on
     */
    retry: Action[];
  };
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
    /**
     * each option in the owner's language, when one differs
     */
    labels?: string[];
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
  /**
   * the lanes of a board: the field of its items and the values of its enum with their labels
   */
  board?: {
    field: string;
    lanes: {
      value: string;
      label: string;
    }[];
  };
  items?: {
    id: unknown;
    title: string;
    /**
     * the value of the board field of the item
     */
    lane?: string;
    text?: string;
    meta?: string;
    icon?: string;
    /**
     * the time the line counts from, shown as how long ago
     */
    since?: string;
    details?: string;
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
  /**
   * how many items the limit of the source left out of a list or a table
   */
  more?: number;
  value?: unknown;
  /**
   * The buttons of a stat; a click on it runs the first that opens a screen.
   */
  actions?: Action[];
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
    actions?: Action[];
  }[];
  /**
   * the log of a process, newest first
   */
  lines?: {
    at: string;
    kind: string;
    id?: string;
    title?: string;
    status?: string;
    text?: string;
    delete?: Action2;
  }[];
  /**
   * when a process still going started
   */
  since?: string;
  say?: Action3;
}
/**
 * A button: a command call or a move to a screen. verb names a built-in label the client translates.
 */
export interface Action2 {
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
   * With confirm, a read command the client calls with params and lists before the yes: what the call deletes for good, as kind and name; the yes waits until the list came.
   */
  parts?: string;
  /**
   * Values the owner picks the input from.
   */
  options?: string[];
  /**
   * With a command, the result names a chat topic, which the client opens; alone, the button opens the chat of the master in a new topic.
   */
  chat?: boolean;
  undo?: Action1;
}
/**
 * A button: a command call or a move to a screen. verb names a built-in label the client translates.
 */
export interface Action3 {
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
   * With confirm, a read command the client calls with params and lists before the yes: what the call deletes for good, as kind and name; the yes waits until the list came.
   */
  parts?: string;
  /**
   * Values the owner picks the input from.
   */
  options?: string[];
  /**
   * With a command, the result names a chat topic, which the client opens; alone, the button opens the chat of the master in a new topic.
   */
  chat?: boolean;
  undo?: Action1;
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
  /**
   * Values of fields the form does not show and sends as they are
   */
  fixed?: {};
  readonly?: string[];
  record?: number;
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
  /**
   * Bytes of the text, given in its place to a viewer other than the owner
   */
  size?: number;
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
  /**
   * The params of the block, resolved
   */
  params?: {};
  /**
   * The module whose files the block reads; none on a screen of deck
   */
  project?: string;
  name?: string;
  /**
   * Bytes of the HTML, given in its place to a viewer other than the owner
   */
  size?: number;
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
    /**
     * the files the owner attached, or that the reply and the processes it started made
     */
    files?: {
      id: number;
      name: string;
      type?: string;
      size?: number;
      url?: string;
    }[];
    /**
     * the screen the owner wrote it on: project, screen, record, process
     */
    screen?: {};
    reply?: Reply;
    created_at: string;
    /**
     * how a reply of the master went
     */
    work?: {
      provider: string;
      model?: string;
      cost_usd?: number;
      /**
       * what its calls on a subscription would cost, an estimate
       */
      subscription_usd?: number;
      seconds: number;
      steps: number;
      processes: {
        id: number;
        project: string;
        command: string;
        state: string;
        open: Action;
        /**
         * the state in the owner's words
         */
        word?: string;
        tone?: Tone;
        /**
         * it waits for the owner or its provider rather than works
         */
        waits?: boolean;
      }[];
    };
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
     * when the reply last started work
     */
    started_at?: string;
    /**
     * the state in the owner's words
     */
    word?: string;
    tone?: Tone;
    /**
     * it waits for the owner or its provider rather than works
     */
    waits?: boolean;
    processes?: {
      id: number;
      project: string;
      command: string;
      state: string;
      open: Action;
      /**
       * the state in the owner's words
       */
      word?: string;
      tone?: Tone;
      /**
       * it waits for the owner or its provider rather than works
       */
      waits?: boolean;
    }[];
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
