// Code generated from schemas/catalog.json by scripts/catalog-types.mjs. DO NOT EDIT.

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
  action?: Action;
  data?: Data;
  ActionsValue?: ActionsValue;
  CardValue?: CardValue;
  StatValue?: StatValue;
  BoardValue?: BoardValue;
  FormValue?: FormValue;
  DocumentValue?: DocumentValue;
  CustomValue?: CustomValue;
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
}
/**
 * This interface was referenced by `Catalog`'s JSON-Schema
 * via the `definition` "data".
 */
export interface Data {
  state: "ready" | "empty" | "error";
  error?: string;
  /**
   * Choices the owner narrows the block by; the client sends the picked ones back to deck.screen.
   */
  filters?: {
    field: string;
    value?: string;
    options: string[];
  }[];
  rows?: {
    id: unknown;
    cells: unknown[];
    actions?: Action[];
  }[];
  items?: {
    id: unknown;
    title: string;
    text?: string;
    meta?: string;
    details?: string;
    /**
     * text is markdown
     */
    markdown?: boolean;
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
  }[];
  actions: Action[];
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
