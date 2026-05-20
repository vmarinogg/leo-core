// Sample TSX file for Cartographer AST extraction tests.
import React from 'react';

export interface ButtonProps {
  label: string;
  onClick: () => void;
}

export class FormManager {
  submit() {}
}

export function Button({ label, onClick }: ButtonProps) {
  return <button onClick={onClick}>{label}</button>;
}

export const Card = ({ title }: { title: string }) => (
  <div>{title}</div>
);
