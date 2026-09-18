import { render } from "preact";
import { App } from "./App";
import "./style.css";

const root = document.getElementById("app")!;

// 使用 Preact 的 render 方法，以 JSX 标签的形式 <App /> 挂载
render(<App />, root);