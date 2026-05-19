export interface Board {
  id: string;
  lists: any[];
}

export function getBoard(id: string): Board;
