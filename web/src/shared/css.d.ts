// The injected script imports CSS as text (esbuild --loader:.css=text) to put
// it into a shadow root; the dashboard imports it for its side effect.
declare module "*.css" {
  const text: string;
  export default text;
}
