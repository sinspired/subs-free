interface Props {
    msg: string;
    visible: boolean;
}

export function Toast({ msg, visible }: Props) {
    return (
        <div class={`toast ${visible ? 'show' : ''}`}>
            {msg}
        </div>
    );
}
