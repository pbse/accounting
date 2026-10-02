import { useState, type ReactNode } from "react";
import { AgentComposer } from "@close/ui/components/AgentComposer";
import { askAssistant } from "./assistantStore";

// "Ask the agent" at the top of a page: a question typed here or a suggestion picked opens the agent panel and asks it
// there, in the close's conversation or, without a close, the firm's. Nothing runs from the page itself: the panel
// answers, or shows what it would do and waits for the person to confirm.
export function AskAgent({
  closeId = "",
  description,
  placeholder,
  suggestions,
  children,
}: {
  closeId?: string;
  description: string;
  placeholder: string;
  suggestions: readonly string[];
  children?: ReactNode;
}) {
  const [question, setQuestion] = useState("");
  const ask = (text: string) => {
    askAssistant(text, closeId);
    setQuestion("");
  };
  return (
    <AgentComposer
      title="Ask the agent"
      description={description}
      value={question}
      onChange={setQuestion}
      onSubmit={() => ask(question.trim())}
      busy={false}
      placeholder={placeholder}
      submitLabel="Ask"
      suggestions={suggestions}
      onSuggestion={ask}
    >
      {children}
    </AgentComposer>
  );
}
