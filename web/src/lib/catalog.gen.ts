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
  picture?: Picture;
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
  ScreenProps?: ScreenProps;
  TableProps?: TableProps;
  ListProps?: ListProps;
  CardProps?: CardProps;
  StatProps?: StatProps;
  ChartProps?: ChartProps;
  MediaProps?: MediaProps;
  ActionsProps?: ActionsProps;
  LogProps?: LogProps;
  FormProps?: FormProps;
  AppearanceProps?: AppearanceProps;
  DocumentProps?: DocumentProps;
  CustomProps?: CustomProps;
  ChatProps?: ChatProps;
}
/**
 * a picture on a card or a folder tile: where the client reads its light copy in the light and the dark scheme, and its main color in each, shown while it loads
 *
 * This interface was referenced by `Catalog`'s JSON-Schema
 * via the `definition` "picture".
 */
export interface Picture {
  light: string;
  dark?: string;
  color?: string;
  dark_color?: string;
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
   * The fields a custom block's call fills beside params; the host refuses any other.
   */
  from_block?: string[];
  /**
   * The fields of from_block that name a record: the host takes only the id of an item of the block or of the record of its page.
   */
  from_items?: string[];
  /**
   * The client asks the owner before the call: a button of deck that deletes or revokes.
   */
  confirm?: boolean;
  /**
   * The call goes a few seconds after the press, while a note on the screen offers to take it back; for an answer the owner may change their mind about, such as a decision.
   */
  hold?: boolean;
  /**
   * With confirm, a read command the client calls with params and lists before the yes: what the call deletes for good, as kind and name; the yes waits until the list came.
   */
  parts?: string;
  /**
   * Values the owner picks the input from.
   */
  options?: string[];
  /**
   * Each option in the owner's language, when one differs from its value.
   */
  labels?: string[];
  /**
   * The table column with the same pick draws the button as a picker of its options in the cell.
   */
  pick?: string;
  /**
   * With a command, the result names a chat topic, which the client opens; alone it opens the chat in a new topic, which the button of Deki does on every screen, so a save refuses it.
   */
  chat?: boolean;
  /**
   * With a command, the result names a stored file (file), which the client downloads.
   */
  download?: boolean;
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
   * The fields a custom block's call fills beside params; the host refuses any other.
   */
  from_block?: string[];
  /**
   * The fields of from_block that name a record: the host takes only the id of an item of the block or of the record of its page.
   */
  from_items?: string[];
  /**
   * The client asks the owner before the call: a button of deck that deletes or revokes.
   */
  confirm?: boolean;
  /**
   * The call goes a few seconds after the press, while a note on the screen offers to take it back; for an answer the owner may change their mind about, such as a decision.
   */
  hold?: boolean;
  /**
   * With confirm, a read command the client calls with params and lists before the yes: what the call deletes for good, as kind and name; the yes waits until the list came.
   */
  parts?: string;
  /**
   * Values the owner picks the input from.
   */
  options?: string[];
  /**
   * Each option in the owner's language, when one differs from its value.
   */
  labels?: string[];
  /**
   * The table column with the same pick draws the button as a picker of its options in the cell.
   */
  pick?: string;
  /**
   * With a command, the result names a chat topic, which the client opens; alone it opens the chat in a new topic, which the button of Deki does on every screen, so a save refuses it.
   */
  chat?: boolean;
  /**
   * With a command, the result names a stored file (file), which the client downloads.
   */
  download?: boolean;
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
  /**
   * the model its replies go on, when the owner picked one
   */
  model?: string;
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
   * what a reply that broke off or the owner stopped left
   */
  cut?: {
    /**
     * the owner stopped it; its one retry goes on on its own provider
     */
    stopped?: boolean;
    /**
     * why it broke off, in words
     */
    reason?: string;
    /**
     * the calls of tools it made
     */
    steps: number;
    /**
     * the last thing it said
     */
    said?: string;
    /**
     * the calls it made that act, in order
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
     * the words the owner looks for in the fields of search, sent as the value of the field ~
     */
    search?: boolean;
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
   * the buttons in the head of a list
   */
  head?: Action[];
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
    /**
     * what the item is, said in the head of its card in a focus list
     */
    kind?: string;
    cover?: Picture;
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
    /**
     * the light copy of a picture, shown small; the file itself only in a viewer
     */
    thumb?: string;
    width?: number;
    height?: number;
    /**
     * the main color of the picture, shown while it loads
     */
    color?: string;
    /**
     * the icon of a line that is no file; its tile presses the primary button
     */
    icon?: string;
    /**
     * a short line under the title
     */
    meta?: string;
    cover?: Picture;
    error?: string;
    actions?: Action[];
  }[];
  /**
   * the steps of the folder a file manager shows, its root first, each with the button that opens it
   */
  crumbs?: {
    label: string;
    action?: Action;
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
   * The fields a custom block's call fills beside params; the host refuses any other.
   */
  from_block?: string[];
  /**
   * The fields of from_block that name a record: the host takes only the id of an item of the block or of the record of its page.
   */
  from_items?: string[];
  /**
   * The client asks the owner before the call: a button of deck that deletes or revokes.
   */
  confirm?: boolean;
  /**
   * The call goes a few seconds after the press, while a note on the screen offers to take it back; for an answer the owner may change their mind about, such as a decision.
   */
  hold?: boolean;
  /**
   * With confirm, a read command the client calls with params and lists before the yes: what the call deletes for good, as kind and name; the yes waits until the list came.
   */
  parts?: string;
  /**
   * Values the owner picks the input from.
   */
  options?: string[];
  /**
   * Each option in the owner's language, when one differs from its value.
   */
  labels?: string[];
  /**
   * The table column with the same pick draws the button as a picker of its options in the cell.
   */
  pick?: string;
  /**
   * With a command, the result names a chat topic, which the client opens; alone it opens the chat in a new topic, which the button of Deki does on every screen, so a save refuses it.
   */
  chat?: boolean;
  /**
   * With a command, the result names a stored file (file), which the client downloads.
   */
  download?: boolean;
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
   * The fields a custom block's call fills beside params; the host refuses any other.
   */
  from_block?: string[];
  /**
   * The fields of from_block that name a record: the host takes only the id of an item of the block or of the record of its page.
   */
  from_items?: string[];
  /**
   * The client asks the owner before the call: a button of deck that deletes or revokes.
   */
  confirm?: boolean;
  /**
   * The call goes a few seconds after the press, while a note on the screen offers to take it back; for an answer the owner may change their mind about, such as a decision.
   */
  hold?: boolean;
  /**
   * With confirm, a read command the client calls with params and lists before the yes: what the call deletes for good, as kind and name; the yes waits until the list came.
   */
  parts?: string;
  /**
   * Values the owner picks the input from.
   */
  options?: string[];
  /**
   * Each option in the owner's language, when one differs from its value.
   */
  labels?: string[];
  /**
   * The table column with the same pick draws the button as a picker of its options in the cell.
   */
  pick?: string;
  /**
   * With a command, the result names a chat topic, which the client opens; alone it opens the chat in a new topic, which the button of Deki does on every screen, so a save refuses it.
   */
  chat?: boolean;
  /**
   * With a command, the result names a stored file (file), which the client downloads.
   */
  download?: boolean;
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
  send?: Action4;
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
 * A button: a command call or a move to a screen. verb names a built-in label the client translates.
 */
export interface Action4 {
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
   * The fields a custom block's call fills beside params; the host refuses any other.
   */
  from_block?: string[];
  /**
   * The fields of from_block that name a record: the host takes only the id of an item of the block or of the record of its page.
   */
  from_items?: string[];
  /**
   * The client asks the owner before the call: a button of deck that deletes or revokes.
   */
  confirm?: boolean;
  /**
   * The call goes a few seconds after the press, while a note on the screen offers to take it back; for an answer the owner may change their mind about, such as a decision.
   */
  hold?: boolean;
  /**
   * With confirm, a read command the client calls with params and lists before the yes: what the call deletes for good, as kind and name; the yes waits until the list came.
   */
  parts?: string;
  /**
   * Values the owner picks the input from.
   */
  options?: string[];
  /**
   * Each option in the owner's language, when one differs from its value.
   */
  labels?: string[];
  /**
   * The table column with the same pick draws the button as a picker of its options in the cell.
   */
  pick?: string;
  /**
   * With a command, the result names a chat topic, which the client opens; alone it opens the chat in a new topic, which the button of Deki does on every screen, so a save refuses it.
   */
  chat?: boolean;
  /**
   * With a command, the result names a stored file (file), which the client downloads.
   */
  download?: boolean;
  undo?: Action1;
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
  save?: Action5;
  revisions: {
    revision: number;
    author: string;
    reason?: string;
    created_at: string;
    revert?: Action6;
  }[];
}
/**
 * A button: a command call or a move to a screen. verb names a built-in label the client translates.
 */
export interface Action5 {
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
   * The fields a custom block's call fills beside params; the host refuses any other.
   */
  from_block?: string[];
  /**
   * The fields of from_block that name a record: the host takes only the id of an item of the block or of the record of its page.
   */
  from_items?: string[];
  /**
   * The client asks the owner before the call: a button of deck that deletes or revokes.
   */
  confirm?: boolean;
  /**
   * The call goes a few seconds after the press, while a note on the screen offers to take it back; for an answer the owner may change their mind about, such as a decision.
   */
  hold?: boolean;
  /**
   * With confirm, a read command the client calls with params and lists before the yes: what the call deletes for good, as kind and name; the yes waits until the list came.
   */
  parts?: string;
  /**
   * Values the owner picks the input from.
   */
  options?: string[];
  /**
   * Each option in the owner's language, when one differs from its value.
   */
  labels?: string[];
  /**
   * The table column with the same pick draws the button as a picker of its options in the cell.
   */
  pick?: string;
  /**
   * With a command, the result names a chat topic, which the client opens; alone it opens the chat in a new topic, which the button of Deki does on every screen, so a save refuses it.
   */
  chat?: boolean;
  /**
   * With a command, the result names a stored file (file), which the client downloads.
   */
  download?: boolean;
  undo?: Action1;
}
/**
 * A button: a command call or a move to a screen. verb names a built-in label the client translates.
 */
export interface Action6 {
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
   * The fields a custom block's call fills beside params; the host refuses any other.
   */
  from_block?: string[];
  /**
   * The fields of from_block that name a record: the host takes only the id of an item of the block or of the record of its page.
   */
  from_items?: string[];
  /**
   * The client asks the owner before the call: a button of deck that deletes or revokes.
   */
  confirm?: boolean;
  /**
   * The call goes a few seconds after the press, while a note on the screen offers to take it back; for an answer the owner may change their mind about, such as a decision.
   */
  hold?: boolean;
  /**
   * With confirm, a read command the client calls with params and lists before the yes: what the call deletes for good, as kind and name; the yes waits until the list came.
   */
  parts?: string;
  /**
   * Values the owner picks the input from.
   */
  options?: string[];
  /**
   * Each option in the owner's language, when one differs from its value.
   */
  labels?: string[];
  /**
   * The table column with the same pick draws the button as a picker of its options in the cell.
   */
  pick?: string;
  /**
   * With a command, the result names a chat topic, which the client opens; alone it opens the chat in a new topic, which the button of Deki does on every screen, so a save refuses it.
   */
  chat?: boolean;
  /**
   * With a command, the result names a stored file (file), which the client downloads.
   */
  download?: boolean;
  undo?: Action1;
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
 * a part of the chat: the topic picked with its messages, the topics, or the turn
 *
 * This interface was referenced by `Catalog`'s JSON-Schema
 * via the `definition` "ChatValue".
 */
export interface ChatValue {
  topic?: Topic;
  topics?: Topic[];
  archived?: Topic[];
  messages?: {
    id: number;
    /**
     * deck: a message deck wrote itself, how a work started from the topic went
     */
    role: "owner" | "master" | "deck";
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
      /**
       * the light copy of a picture, shown small; the file itself only in a viewer
       */
      thumb?: string;
      width?: number;
      height?: number;
      /**
       * the main color of the picture, shown while it loads
       */
      color?: string;
    }[];
    /**
     * the screen the owner wrote it on: project, screen, record, process
     */
    screen?: {};
    reply?: Reply;
    created_at: string;
    delete?: Action7;
    /**
     * on a message of deck: the work it tells of, in the state it was in; its text is the result, or what it waits for
     */
    told?: {
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
      waits?: boolean;
      /**
       * why a failed work failed, in the owner's words
       */
      reason?: string;
      /**
       * what deck wrote of the failure
       */
      error?: string;
    };
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
  questions?: {
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
  send?: Action;
  archive?: Action;
  delete?: Action;
  /**
   * the models the master's replies may go on
   */
  models?: string[];
  /**
   * the price of each model in words, such as subscription or free
   */
  prices?: {
    [k: string]: string;
  };
  /**
   * the master's own model, which a topic without one takes
   */
  model?: string;
  choose?: Action;
  /**
   * of a topic without messages: commands of the module of the owner's screen, as texts to put in the field
   */
  suggest?: {
    icon: string;
    text: string;
  }[];
  /**
   * what the turn going on and the processes it started wait for the owner to decide, its questions apart: items of deck.asks_list with their buttons
   */
  asks?: {
    id: number;
    kind: string;
    project?: string;
    who?: string;
    what: string;
    why?: string;
    where?: string;
    text?: string;
    command?: string;
    by?: string;
    payload?: unknown;
    expires_at?: string;
    expired?: boolean;
    actions: Action[];
  }[];
}
/**
 * A button: a command call or a move to a screen. verb names a built-in label the client translates.
 */
export interface Action7 {
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
   * The fields a custom block's call fills beside params; the host refuses any other.
   */
  from_block?: string[];
  /**
   * The fields of from_block that name a record: the host takes only the id of an item of the block or of the record of its page.
   */
  from_items?: string[];
  /**
   * The client asks the owner before the call: a button of deck that deletes or revokes.
   */
  confirm?: boolean;
  /**
   * The call goes a few seconds after the press, while a note on the screen offers to take it back; for an answer the owner may change their mind about, such as a decision.
   */
  hold?: boolean;
  /**
   * With confirm, a read command the client calls with params and lists before the yes: what the call deletes for good, as kind and name; the yes waits until the list came.
   */
  parts?: string;
  /**
   * Values the owner picks the input from.
   */
  options?: string[];
  /**
   * Each option in the owner's language, when one differs from its value.
   */
  labels?: string[];
  /**
   * The table column with the same pick draws the button as a picker of its options in the cell.
   */
  pick?: string;
  /**
   * With a command, the result names a chat topic, which the client opens; alone it opens the chat in a new topic, which the button of Deki does on every screen, so a save refuses it.
   */
  chat?: boolean;
  /**
   * With a command, the result names a stored file (file), which the client downloads.
   */
  download?: boolean;
  undo?: Action1;
}
/**
 * This interface was referenced by `Catalog`'s JSON-Schema
 * via the `definition` "ScreenProps".
 */
export interface ScreenProps {
  title?: string;
  icon?: string;
  span?: 1 | 2 | "full";
  description?: string;
  children: string[];
  /**
   * The dialogs of the screen, Modals whose buttons stand beside its title.
   */
  dialogs?: string[];
  /**
   * Of a tab of Tabs: its title as written, its name in the address in every language.
   */
  key?: string;
  /**
   * Of a tab of Tabs: a count of deck the client shows beside its title, kept fresh, such as the open failures.
   */
  count?: "failures";
  /**
   * Of the window of a Modal: the name of its dialog, ?dialog=<name> in the address opens it.
   */
  name?: string;
  /**
   * The cells of the grid a standard child narrower than the screen takes, by its id.
   */
  spans?: {
    [k: string]: 1 | 2;
  };
  /**
   * Of the root screen: the event topics after which the client reads it again; it ignores the others.
   */
  topics?: string[];
}
/**
 * This interface was referenced by `Catalog`'s JSON-Schema
 * via the `definition` "TableProps".
 */
export interface TableProps {
  title?: string;
  icon?: string;
  span?: 1 | 2 | "full";
  description?: string;
  columns: {
    label: string;
    format?: string;
    currency?: string;
    /**
     * The button of each row whose pick is the same is the one the cell picks the input of.
     */
    pick?: string;
  }[];
  data?: Data;
}
/**
 * This interface was referenced by `Catalog`'s JSON-Schema
 * via the `definition` "ListProps".
 */
export interface ListProps {
  title?: string;
  icon?: string;
  span?: 1 | 2 | "full";
  description?: string;
  cards?: boolean;
  focus?: boolean;
  data?: Data;
}
/**
 * This interface was referenced by `Catalog`'s JSON-Schema
 * via the `definition` "CardProps".
 */
export interface CardProps {
  title?: string;
  icon?: string;
  span?: 1 | 2 | "full";
  description?: string;
  data?: Data;
}
/**
 * This interface was referenced by `Catalog`'s JSON-Schema
 * via the `definition` "StatProps".
 */
export interface StatProps {
  title?: string;
  icon?: string;
  span?: 1 | 2 | "full";
  description?: string;
  format?: string;
  currency?: string;
  data?: Data;
}
/**
 * This interface was referenced by `Catalog`'s JSON-Schema
 * via the `definition` "ChartProps".
 */
export interface ChartProps {
  title?: string;
  icon?: string;
  span?: 1 | 2 | "full";
  description?: string;
  kind?: string;
  format?: string;
  currency?: string;
  data?: Data;
}
/**
 * This interface was referenced by `Catalog`'s JSON-Schema
 * via the `definition` "MediaProps".
 */
export interface MediaProps {
  title?: string;
  icon?: string;
  span?: 1 | 2 | "full";
  description?: string;
  data?: Data;
}
/**
 * This interface was referenced by `Catalog`'s JSON-Schema
 * via the `definition` "ActionsProps".
 */
export interface ActionsProps {
  title?: string;
  icon?: string;
  span?: 1 | 2 | "full";
  description?: string;
  data?: Data;
}
/**
 * This interface was referenced by `Catalog`'s JSON-Schema
 * via the `definition` "LogProps".
 */
export interface LogProps {
  title?: string;
  icon?: string;
  span?: 1 | 2 | "full";
  description?: string;
  data?: Data;
}
/**
 * This interface was referenced by `Catalog`'s JSON-Schema
 * via the `definition` "FormProps".
 */
export interface FormProps {
  title?: string;
  icon?: string;
  span?: 1 | 2 | "full";
  description?: string;
  data?: Data;
}
/**
 * This interface was referenced by `Catalog`'s JSON-Schema
 * via the `definition` "AppearanceProps".
 */
export interface AppearanceProps {
  title?: string;
  icon?: string;
  span?: 1 | 2 | "full";
  description?: string;
  part?: "look" | "pushes";
  data?: Data;
}
/**
 * This interface was referenced by `Catalog`'s JSON-Schema
 * via the `definition` "DocumentProps".
 */
export interface DocumentProps {
  title?: string;
  icon?: string;
  span?: 1 | 2 | "full";
  description?: string;
  data?: Data;
}
/**
 * This interface was referenced by `Catalog`'s JSON-Schema
 * via the `definition` "CustomProps".
 */
export interface CustomProps {
  title?: string;
  icon?: string;
  span?: 1 | 2 | "full";
  description?: string;
  data?: Data;
}
/**
 * This interface was referenced by `Catalog`'s JSON-Schema
 * via the `definition` "ChatProps".
 */
export interface ChatProps {
  title?: string;
  icon?: string;
  span?: 1 | 2 | "full";
  description?: string;
  data?: Data;
  topics?: Data;
  turn?: Data;
}
